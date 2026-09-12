// Package backend is the Go business application mounted behind the migration
// Gateway. Its handlers deliberately speak the existing Result envelope.
package backend

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/auth"
	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/httpclient"
	"github.com/shijie152/ani-rss/go-backend/internal/media"
	"github.com/shijie152/ani-rss/go-backend/internal/metadata"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/ownership"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

type Options struct {
	ConfigDir        string
	Version          string
	Logger           *slog.Logger
	OwnershipDomains []string
}

type App struct {
	store         *store.JSONStore
	config        *appconfig.Manager
	auth          *auth.Authenticator
	ownership     *ownership.Manager
	subscriptions *subscription.Service
	configDir     string
	logger        *slog.Logger
	mu            sync.RWMutex
	refreshMu     sync.Mutex
	ownedDomains  []string
	stateRequired bool
}

func New(options Options) (*App, error) {
	jsonStore, err := store.NewJSONStore(options.ConfigDir)
	if err != nil {
		return nil, err
	}
	manager, err := appconfig.NewManager(jsonStore)
	if err != nil {
		return nil, err
	}
	items, err := jsonStore.LoadSubscriptions()
	if err != nil {
		return nil, err
	}
	if err := subscription.ValidateItems(items); err != nil {
		return nil, fmt.Errorf("订阅数据校验失败: %w", err)
	}
	locks, err := ownership.NewManager(filepath.Join(jsonStore.Directory(), "locks"))
	if err != nil {
		return nil, err
	}
	ownedDomains := make([]string, 0, len(options.OwnershipDomains))
	for _, domain := range options.OwnershipDomains {
		if err := locks.Acquire(domain, "go"); err != nil {
			if errors.Is(err, ownership.ErrAlreadyOwned) {
				// A Java transition runtime may still own this domain. Keep the
				// process alive and let the Gateway route that domain to Java.
				continue
			}
			locks.Close()
			return nil, err
		}
		ownedDomains = append(ownedDomains, domain)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	stateRequired := false
	for _, domain := range options.OwnershipDomains {
		if domain == "state" {
			stateRequired = true
			break
		}
	}
	if options.Version != "" {
		_, ownsState := locks.Owner("state")
		if !stateRequired || ownsState {
			if err := manager.Update(model.Config{"version": options.Version}); err != nil {
				locks.Close()
				return nil, err
			}
		}
	}
	if stateRequired {
		if _, owned := locks.Owner("state"); !owned {
			// Do not retain write-capable domain locks after losing the shared
			// state lock. Otherwise Java cannot acquire the domains that the
			// Gateway is about to route back to it.
			for _, domain := range append([]string(nil), ownedDomains...) {
				switch domain {
				case "runtime", "subscriptions", "rss", "media":
					_ = locks.Release(domain)
				}
			}
			filtered := ownedDomains[:0]
			for _, domain := range ownedDomains {
				switch domain {
				case "runtime", "subscriptions", "rss", "media":
					continue
				default:
					filtered = append(filtered, domain)
				}
			}
			ownedDomains = filtered
		}
	}
	return &App{store: jsonStore, config: manager, auth: auth.New(manager), ownership: locks, subscriptions: subscription.NewService(jsonStore, manager, items), configDir: jsonStore.Directory(), logger: logger, ownedDomains: ownedDomains, stateRequired: stateRequired}, nil
}

func (a *App) Config() *appconfig.Manager { return a.config }
func (a *App) Store() store.Store         { return a.store }
func (a *App) Auth() *auth.Authenticator  { return a.auth }

// OwnedDomains returns only domains successfully claimed by this process.
// Conflicting domains remain on the Java fallback during migration.
func (a *App) OwnedDomains() []string {
	owned := append([]string(nil), a.ownedDomains...)
	if a.stateRequired {
		if _, ok := a.ownership.Owner("state"); !ok {
			// Config, subscriptions, RSS and media processing can all mutate the
			// shared JSON state. Keep only read-only source discovery available
			// while Java remains the state writer.
			filtered := owned[:0]
			for _, domain := range owned {
				if domain != "runtime" && domain != "subscriptions" && domain != "rss" && domain != "media" {
					filtered = append(filtered, domain)
				}
			}
			owned = filtered
		}
	}
	return owned
}

// AcquireDomains must be called before starting Go schedulers. A caller may
// select only the domains it has cut over; no Go task is started for the rest.
func (a *App) AcquireDomains(domains ...string) error {
	for _, domain := range domains {
		if err := a.ownership.Acquire(domain, "go"); err != nil {
			for _, acquired := range domains {
				if acquired == domain {
					break
				}
				_ = a.ownership.Release(acquired)
			}
			return err
		}
	}
	return nil
}

func (a *App) Close() { a.ownership.Close() }

// RunSchedulers runs the scheduler domains owned by this Go process. During
// the first migration slice RSS is the only periodic business task migrated;
// rename and maintenance remain Java-owned until their own cutovers.
//
// The method blocks until ctx is cancelled and is intended to run in one
// goroutine from the command entrypoint.
func (a *App) RunSchedulers(ctx context.Context) {
	if a.stateRequired {
		if _, owned := a.ownership.Owner("state"); !owned {
			return
		}
	}
	if _, owned := a.ownership.Owner("rss"); !owned {
		return
	}
	interval := time.Duration(appconfig.Int(a.config.Snapshot(), "rssSleepMinutes")) * time.Minute
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	run := func() {
		if !appconfig.Bool(a.config.Snapshot(), "rss") {
			return
		}
		if err := a.refreshAllSubscriptions(ctx); err != nil && !errors.Is(err, context.Canceled) {
			a.logger.Warn("scheduled RSS refresh failed", "error", err)
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (a *App) Routes() []gateway.Route {
	runtime := func(method, path string, handler http.Handler) gateway.Route {
		return gateway.Route{Domain: "runtime", Method: method, Path: path, Handler: handler}
	}
	subscriptions := func(method, path string, handler http.Handler) gateway.Route {
		return gateway.Route{Domain: "subscriptions", Method: method, Path: path, Handler: handler}
	}
	return []gateway.Route{
		runtime(http.MethodGet, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodPost, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodPost, "/api/login", http.HandlerFunc(a.login)),
		runtime(http.MethodPost, "/api/config", a.protected(a.configGet)),
		runtime(http.MethodPost, "/api/setConfig", a.protected(a.configSet)),
		runtime(http.MethodPost, "/api/testIpWhitelist", http.HandlerFunc(a.testIPWhitelist)),
		runtime(http.MethodGet, "/api/custom.js", http.HandlerFunc(a.customJS)),
		runtime(http.MethodGet, "/api/custom.css", http.HandlerFunc(a.customCSS)),
		runtime(http.MethodPost, "/api/testProxy", a.protected(a.testProxy)),
		subscriptions(http.MethodPost, "/api/listAni", a.protected(a.listAni)),
		subscriptions(http.MethodPost, "/api/addAni", a.protected(a.addAni)),
		subscriptions(http.MethodPost, "/api/setAni", a.protected(a.setAni)),
		subscriptions(http.MethodPost, "/api/deleteAni", a.protected(a.deleteAni)),
		subscriptions(http.MethodPost, "/api/batchEnable", a.protected(a.batchEnable)),
		subscriptions(http.MethodPost, "/api/updateTotalEpisodeNumber", a.protected(a.updateTotalEpisodeNumber)),
		subscriptions(http.MethodPost, "/api/importAni", a.protected(a.importAni)),
		subscriptions(http.MethodPost, "/api/downloadPath", a.protected(a.downloadPath)),
		{Domain: "sources", Method: http.MethodPost, Path: "/api/mikan", Handler: a.protected(a.mikan)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/mikanGroup", Handler: a.protected(a.mikanGroup)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/aniBT", Handler: a.protected(a.aniBT)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/aniBTGroup", Handler: a.protected(a.aniBTGroup)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/animeGardenList", Handler: a.protected(a.animeGardenList)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/animeGardenGroup", Handler: a.protected(a.animeGardenGroup)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/searchBgm", Handler: a.protected(a.searchBgm)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/getAniBySubjectId", Handler: a.protected(a.getAniBySubjectID)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/getBgmTitle", Handler: a.protected(a.getBGMTitle)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/rssToAni", Handler: a.protected(a.rssToAni)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/refreshAll", Handler: a.protected(a.refreshAll)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/refreshAni", Handler: a.protected(a.refreshAni)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/previewAni", Handler: a.protected(a.previewAni)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/deleteTorrent", Handler: a.protected(a.deleteTorrent)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/torrentsInfos", Handler: a.protected(a.torrentsInfos)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/downloadLoginTest", Handler: a.protected(a.downloadLoginTest)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/scrape", Handler: a.protected(a.scrape)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/batchScrape", Handler: a.protected(a.batchScrape)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/refreshCover", Handler: a.protected(a.refreshCover)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/getThemoviedbName", Handler: a.protected(a.getThemoviedbName)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/getThemoviedbGroup", Handler: a.protected(a.getThemoviedbGroup)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/playList", Handler: a.protected(a.playList)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/getSubtitles", Handler: a.protected(a.getSubtitles)},
		{Domain: "media", Method: http.MethodGet, Path: "/api/file", Handler: a.protected(a.file)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/upload", Handler: a.protected(a.upload)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/uploadAndRead", Handler: a.protected(a.uploadAndRead)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/uploadAndReadToBase64", Handler: a.protected(a.uploadAndReadBase64)},
	}
}

func (a *App) ping(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusOK, nil, "success")
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var input model.Login
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	token, err := a.auth.Login(r, input)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, auth.ErrTooManyAttempts) {
			code = http.StatusForbidden
		}
		writeResult(w, code, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, token, "登录成功")
}

func (a *App) configGet(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusOK, a.config.PublicSnapshot(), "success")
}

func (a *App) configSet(w http.ResponseWriter, r *http.Request) {
	var input model.Config
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "配置格式异常: "+err.Error())
		return
	}
	if err := a.config.Update(input); err != nil {
		a.logger.Warn("config update rejected", "error", err)
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "修改成功")
}

func (a *App) testIPWhitelist(w http.ResponseWriter, r *http.Request) {
	if a.auth.IPWhitelist(r) {
		writeResult(w, http.StatusOK, nil, "success")
		return
	}
	writeResult(w, http.StatusInternalServerError, nil, "error")
}

func (a *App) customJS(w http.ResponseWriter, _ *http.Request) {
	value := appconfig.String(a.config.Snapshot(), "customJs")
	if value == "" {
		value = "// empty js"
	}
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = io.WriteString(w, value)
}

func (a *App) customCSS(w http.ResponseWriter, _ *http.Request) {
	value := appconfig.String(a.config.Snapshot(), "customCss")
	if value == "" {
		value = "/* empty css */"
	}
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = io.WriteString(w, value)
}

func (a *App) testProxy(w http.ResponseWriter, r *http.Request) {
	encoded := strings.ReplaceAll(r.URL.Query().Get("url"), " ", "+")
	targetBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || strings.TrimSpace(string(targetBytes)) == "" {
		writeResult(w, http.StatusInternalServerError, nil, "URL 格式异常")
		return
	}
	target, err := url.Parse(string(targetBytes))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		writeResult(w, http.StatusInternalServerError, nil, "URL 格式异常")
		return
	}
	var input model.Config
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "代理配置格式异常: "+err.Error())
		return
	}
	client, err := httpclient.New(input, time.Duration(appconfig.Int(input, "rssTimeout"))*time.Second)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	started := time.Now()
	result := map[string]any{"status": 0, "title": "", "time": int64(0)}
	response, err := client.Get(target.String())
	if err == nil {
		defer response.Body.Close()
		result["status"] = response.StatusCode
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if readErr == nil {
			result["title"] = pageTitle(string(body))
		}
	}
	result["time"] = time.Since(started).Milliseconds()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, result, err.Error())
		return
	}
	writeResult(w, http.StatusOK, result, "success")
}

func pageTitle(body string) string {
	lower := strings.ToLower(body)
	start := strings.Index(lower, "<title")
	if start < 0 {
		return ""
	}
	start = strings.Index(body[start:], ">")
	if start < 0 {
		return ""
	}
	start += strings.Index(lower, "<title") + 1
	end := strings.Index(strings.ToLower(body[start:]), "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(body[start : start+end])
}

func (a *App) listAni(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusOK, a.subscriptions.List(), "success")
}

func (a *App) addAni(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅格式异常: "+err.Error())
		return
	}
	if err := a.subscriptions.Add(item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "添加订阅成功")
}

func (a *App) setAni(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅格式异常: "+err.Error())
		return
	}
	move, _ := strconv.ParseBool(r.URL.Query().Get("move"))
	if err := a.subscriptions.SetWithMove(item, move); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "修改成功")
}

func (a *App) deleteAni(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if err := decodeJSON(r, &ids); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅列表格式异常: "+err.Error())
		return
	}
	deleteFiles, _ := strconv.ParseBool(r.URL.Query().Get("deleteFiles"))
	if err := a.subscriptions.Delete(ids, deleteFiles); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "删除订阅成功")
}

func (a *App) batchEnable(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if err := decodeJSON(r, &ids); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅列表格式异常: "+err.Error())
		return
	}
	value, err := strconv.ParseBool(r.URL.Query().Get("value"))
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "启用参数异常")
		return
	}
	if err := a.subscriptions.BatchEnable(value, ids); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "修改完成")
}

func (a *App) updateTotalEpisodeNumber(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if err := decodeJSON(r, &ids); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅列表格式异常: "+err.Error())
		return
	}
	force, err := strconv.ParseBool(r.URL.Query().Get("force"))
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "强制参数异常")
		return
	}
	client, err := a.sourceClient()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	resolve := func(_ context.Context, item model.Ani) (int, error) {
		id := source.SubjectID(item.BGMURL)
		if id == "" {
			return 0, errors.New("订阅没有 Bangumi subject")
		}
		return client.SubjectEpisodeCount(id)
	}
	if err := a.subscriptions.UpdateTotalEpisodes(r.Context(), force, ids, resolve); err != nil {
		a.logger.Warn("total episode update partially failed", "error", err)
		// Java starts this operation asynchronously and keeps the UI action
		// successful even when one subject cannot be read.
		writeResult(w, http.StatusOK, map[string]any{"error": err.Error()}, "已开始更新总集数")
		return
	}
	writeResult(w, http.StatusOK, nil, "已开始更新总集数")
}

func (a *App) importAni(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Filename string      `json:"filename"`
		AniList  []model.Ani `json:"aniList"`
		Conflict string      `json:"conflict"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "导入格式异常: "+err.Error())
		return
	}
	if err := a.subscriptions.Import(input.AniList, input.Conflict); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "导入成功")
}

func (a *App) downloadPath(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅格式异常: "+err.Error())
		return
	}
	path, err := a.subscriptions.DownloadPath(item)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, path, "success")
}

func (a *App) sourceClient() (*source.Client, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	return source.New(source.Options{
		MikanHost:       appconfig.String(cfg, "mikanHost"),
		AniBTHost:       defaultString(appconfig.String(cfg, "aniBTHost"), "https://anibt.net"),
		AnimeGardenHost: defaultString(appconfig.String(cfg, "animeGardenHost"), "https://api.animes.garden"),
		BangumiAPI:      defaultString(appconfig.String(cfg, "bgmApi"), "https://api.bgm.tv"),
		HTTPClient:      client,
		Retries:         appconfig.Int(cfg, "downloadRetry"),
		Subscriptions:   a.subscriptions.Items,
	}), nil
}

func (a *App) mikan(w http.ResponseWriter, r *http.Request) {
	var season model.Config
	if r.Body != nil {
		_ = decodeJSON(r, &season)
	}
	client, err := a.sourceClient()
	if err == nil {
		var result map[string]any
		result, err = client.Mikan(r.URL.Query().Get("text"), season)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) mikanGroup(w http.ResponseWriter, r *http.Request) {
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.MikanGroup(r.URL.Query().Get("url"))
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) aniBT(w http.ResponseWriter, r *http.Request) {
	var input model.Config
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result map[string]any
		result, err = client.AniBT(input)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) aniBTGroup(w http.ResponseWriter, r *http.Request) {
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.AniBTGroup(r.URL.Query().Get("bgmId"))
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) animeGardenList(w http.ResponseWriter, r *http.Request) {
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.AnimeGardenList(r.URL.Query().Get("bgmUrl"))
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) animeGardenGroup(w http.ResponseWriter, r *http.Request) {
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.AnimeGardenGroup(r.URL.Query().Get("bgmId"))
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) searchBgm(w http.ResponseWriter, r *http.Request) {
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.SearchBangumi(r.URL.Query().Get("name"))
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) getAniBySubjectID(w http.ResponseWriter, r *http.Request) {
	client, err := a.sourceClient()
	if err == nil {
		var result model.Ani
		result, err = client.SubscriptionFromSubject(r.URL.Query().Get("id"))
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) getBGMTitle(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result string
		result, err = client.BGMTitle(item)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) rssToAni(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL      string `json:"url"`
		Type     string `json:"type"`
		BGMURL   string `json:"bgmUrl"`
		Subgroup string `json:"subgroup"`
		Enable   *bool  `json:"enable"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	id := source.SubjectID(input.BGMURL)
	if id == "" {
		id = source.SubjectID(input.URL)
	}
	client, err := a.sourceClient()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	item, err := client.SubscriptionFromSubject(id)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	item.URL, item.Type, item.Subgroup = input.URL, defaultString(input.Type, "mikan"), input.Subgroup
	if input.Enable == nil {
		item.Enable = true
	} else {
		item.Enable = *input.Enable
	}
	writeResult(w, http.StatusOK, item, "success")
}

func (a *App) newCoordinator() (*rss.Coordinator, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	username := appconfig.String(cfg, "downloadToolUsername")
	password := appconfig.String(cfg, "downloadToolPassword")
	apiKey := ""
	if username == "" {
		apiKey = password
	}
	return &rss.Coordinator{Config: a.config, Subscriptions: a.subscriptions, History: a.store, HTTPClient: client, Retry: appconfig.Int(cfg, "downloadRetry"), QB: &downloader.QBittorrent{
		Host: appconfig.String(cfg, "downloadToolHost"), Username: username, Password: password, APIKey: apiKey, Client: client,
		ContentLayout: appconfig.String(cfg, "qbContentLayout"), UseDownloadPath: appconfig.Bool(cfg, "qbUseDownloadPath"),
		RatioLimit: int64(appconfig.Int(cfg, "ratioLimit")), SeedingTimeLimit: int64(appconfig.Int(cfg, "seedingTimeLimit")),
		InactiveSeedingTimeLimit: int64(appconfig.Int(cfg, "inactiveSeedingTimeLimit")), UpLimit: int64(appconfig.Int(cfg, "upLimit")) * 1024, DlLimit: int64(appconfig.Int(cfg, "dlLimit")) * 1024,
	}}, nil
}

func (a *App) refreshAll(w http.ResponseWriter, r *http.Request) {
	err := a.refreshAllSubscriptions(r.Context())
	if err != nil {
		// Java exposes refreshAll as a best-effort background action. Keep the
		// same successful UI contract while returning diagnostics to callers
		// that inspect the response body.
		a.logger.Warn("RSS refresh partially failed", "error", err)
		writeResult(w, http.StatusOK, map[string]any{"error": err.Error()}, "已开始刷新RSS")
		return
	}
	writeResult(w, http.StatusOK, nil, "已开始刷新RSS")
}

func (a *App) refreshAni(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	var selected model.Ani
	found := false
	for _, item := range a.subscriptions.Items() {
		if item.ID == input.ID {
			selected, found = item, true
			break
		}
	}
	if !found {
		writeResult(w, http.StatusInternalServerError, nil, "订阅不存在")
		return
	}
	err := a.refreshSubscription(r.Context(), selected)
	if err != nil {
		a.logger.Warn("RSS refresh failed", "subscription", selected.ID, "error", err)
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	writeResult(w, http.StatusOK, nil, "已开始刷新RSS")
}

func (a *App) refreshAllSubscriptions(ctx context.Context) error {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	coordinator, err := a.newCoordinator()
	if err != nil {
		return err
	}
	_, err = coordinator.RefreshAll(ctx)
	return err
}

func (a *App) refreshSubscription(ctx context.Context, item model.Ani) error {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	coordinator, err := a.newCoordinator()
	if err != nil {
		return err
	}
	_, err = coordinator.Refresh(ctx, item)
	return err
}

func (a *App) previewAni(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	coordinator, err := a.newCoordinator()
	if err == nil {
		var result map[string]any
		result, err = coordinator.PreviewResult(r.Context(), item)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) deleteTorrent(w http.ResponseWriter, r *http.Request) {
	id, hash := r.URL.Query().Get("id"), r.URL.Query().Get("hash")
	if id == "" || hash == "" {
		writeResult(w, http.StatusInternalServerError, nil, "参数不能为空")
		return
	}
	hashes := map[string]bool{}
	for _, value := range strings.Split(hash, ",") {
		if value = strings.TrimSpace(value); value != "" {
			hashes[strings.ToLower(value)] = true
		}
	}
	resources, err := a.store.LoadResources()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	kept := make([]model.Resource, 0, len(resources))
	for _, value := range resources {
		if hashes[strings.ToLower(value.InfoHash)] {
			continue
		}
		kept = append(kept, value)
	}
	if err := a.store.SaveResources(kept); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "删除完成")
}

func (a *App) torrentsInfos(w http.ResponseWriter, r *http.Request) {
	coordinator, err := a.newCoordinator()
	if err == nil {
		if err = coordinator.QB.Login(r.Context()); err == nil {
			var result []model.Torrent
			result, err = coordinator.QB.Torrents(r.Context())
			if err == nil {
				writeResult(w, http.StatusOK, result, "success")
				return
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) downloadLoginTest(w http.ResponseWriter, r *http.Request) {
	var cfg model.Config
	if err := decodeJSON(r, &cfg); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err == nil {
		username := appconfig.String(cfg, "downloadToolUsername")
		password := appconfig.String(cfg, "downloadToolPassword")
		apiKey := ""
		if username == "" {
			apiKey = password
		}
		err = (&downloader.QBittorrent{Host: appconfig.String(cfg, "downloadToolHost"), Username: username, Password: password, APIKey: apiKey, Client: client}).Login(r.Context())
	}
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "登录失败")
		return
	}
	writeResult(w, http.StatusOK, nil, "登录成功")
}

func (a *App) metadataClient() (*metadata.Client, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	return metadata.New(cfg, client), nil
}

func (a *App) mediaService() (*media.Service, error) {
	client, err := a.metadataClient()
	if err != nil {
		return nil, err
	}
	cfg := a.config.Snapshot()
	requestClient, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	service := media.New(a.config, client, requestClient, func(item model.Ani) (string, error) {
		value, pathErr := a.subscriptions.DownloadPath(item)
		if pathErr != nil {
			return "", pathErr
		}
		return value["downloadPath"].(string), nil
	})
	service.ResolveOther = func(item model.Ani, template string) (string, error) {
		return a.subscriptions.DownloadPathWithTemplate(item, template)
	}
	service.ConfigDir = a.configDir
	return service, nil
}

func (a *App) scrape(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅格式异常: "+err.Error())
		return
	}
	force, _ := strconv.ParseBool(r.URL.Query().Get("force"))
	service, err := a.mediaService()
	if err == nil {
		result, scrapeErr := service.Scrape(r.Context(), &item, force)
		if result.Ani.ID != "" {
			if saveErr := a.subscriptions.Set(result.Ani); scrapeErr == nil && saveErr != nil {
				scrapeErr = saveErr
			}
		}
		err = scrapeErr
	}
	if err != nil {
		a.logger.Warn("media scrape failed", "title", item.Title, "error", err)
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "已开始刮削 "+item.Title)
}

func (a *App) batchScrape(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if err := decodeJSON(r, &ids); err != nil || len(ids) == 0 {
		writeResult(w, http.StatusInternalServerError, nil, "未选择订阅")
		return
	}
	force, _ := strconv.ParseBool(r.URL.Query().Get("force"))
	service, err := a.mediaService()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	failures := []string{}
	count := 0
	for _, item := range a.subscriptions.Items() {
		if !selected[item.ID] {
			continue
		}
		result, scrapeErr := service.Scrape(r.Context(), &item, force)
		if scrapeErr == nil {
			scrapeErr = a.subscriptions.Set(result.Ani)
		}
		if scrapeErr != nil {
			failures = append(failures, item.Title+": "+scrapeErr.Error())
			continue
		}
		count++
	}
	if len(failures) > 0 {
		writeResult(w, http.StatusInternalServerError, map[string]any{"processed": count, "errors": failures}, "批量刮削部分失败")
		return
	}
	writeResult(w, http.StatusOK, nil, fmt.Sprintf("已开始刮削%d个订阅", count))
}

func (a *App) refreshCover(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	service, err := a.mediaService()
	if err == nil {
		var cover string
		cover, err = service.RefreshCover(r.Context(), item.Image, true)
		if err == nil && item.ID != "" {
			item.Cover = cover
			err = a.subscriptions.Set(item)
		}
		if err == nil {
			writeResult(w, http.StatusOK, cover, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "文件为空")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	if len(data) == 64<<20 {
		writeResult(w, http.StatusRequestEntityTooLarge, nil, "文件过大")
		return
	}
	name := fmt.Sprintf("%x", md5.Sum(data))
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" || len(ext) > 10 {
		ext = ".bin"
	}
	relative := filepath.ToSlash(filepath.Join(string(name[0]), name+ext))
	target := filepath.Join(a.configDir, "files", relative)
	if err := writeAtomic(target, data); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, relative, "上传完成")
}

func (a *App) uploadAndRead(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "文件为空")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, string(data), "success")
}

func (a *App) uploadAndReadBase64(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "文件为空")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, base64.StdEncoding.EncodeToString(data), "success")
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".upload-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (a *App) getThemoviedbName(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TMDBID string `json:"tmdbId"`
		Title  string `json:"title"`
		OVA    bool   `json:"ova"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	client, err := a.metadataClient()
	if err == nil {
		item := model.Ani{Title: input.Title, OVA: input.OVA}
		if input.TMDBID != "" {
			item.TMDB = map[string]any{"id": input.TMDBID}
		}
		var value model.Metadata
		var raw map[string]any
		value, raw, err = client.Lookup(r.Context(), item)
		if err == nil {
			writeResult(w, http.StatusOK, map[string]any{"tmdb": raw, "themoviedbName": media.FinalTitle(value, a.config)}, "获取 TMDB 成功")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) getThemoviedbGroup(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	id := ""
	if item.TMDB != nil {
		id = appconfig.String(item.TMDB, "id")
	}
	client, err := a.metadataClient()
	if err == nil {
		var groups []map[string]any
		groups, err = client.Groups(r.Context(), id)
		if err == nil {
			writeResult(w, http.StatusOK, groups, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) playList(w http.ResponseWriter, r *http.Request) {
	var input model.Ani
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	var selected model.Ani
	found := false
	for _, item := range a.subscriptions.Items() {
		if (input.ID != "" && item.ID == input.ID) || (input.ID == "" && input.URL != "" && item.URL == input.URL) {
			selected, found = item, true
			break
		}
	}
	if !found {
		writeResult(w, http.StatusInternalServerError, nil, "订阅不存在")
		return
	}
	path, err := a.subscriptions.DownloadPath(selected)
	if err == nil {
		service, serviceErr := a.mediaService()
		if serviceErr != nil {
			err = serviceErr
		} else {
			var files []model.MediaFile
			files, err = service.PlaybackList(path["downloadPath"].(string))
			if err == nil {
				writeResult(w, http.StatusOK, files, "success")
				return
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) getSubtitles(w http.ResponseWriter, r *http.Request) {
	filename, err := decodeBase64Param(r.URL.Query().Get("filename"))
	if err == nil {
		filename = a.resolveFilePath(filename)
		if _, statErr := os.Stat(filename); statErr == nil && media.IsVideo(filename) && a.allowedMediaPath(filename) {
			subtitles := media.SubtitlesFor(filename)
			if embedded, embeddedErr := media.EmbeddedSubtitles(filename); embeddedErr == nil {
				subtitles = append(subtitles, embedded...)
			}
			writeResult(w, http.StatusOK, subtitles, "success")
			return
		}
		err = errors.New("视频文件不存在")
	}
	writeResult(w, http.StatusInternalServerError, nil, err.Error())
}

func (a *App) file(w http.ResponseWriter, r *http.Request) {
	filename, err := decodeBase64Param(r.URL.Query().Get("filename"))
	filename = a.resolveFilePath(filename)
	if err != nil || !a.allowedMediaPath(filename) {
		writeResult(w, http.StatusForbidden, nil, "不允许访问")
		return
	}
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() {
		writeResult(w, http.StatusNotFound, nil, "文件不存在")
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Disposition", `inline; filename="`+url.PathEscape(filepath.Base(filename))+`"`)
	w.Header().Set("Content-Type", contentType)
	if strings.HasPrefix(contentType, "video/") {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	file, openErr := os.Open(filename)
	if openErr != nil {
		writeResult(w, http.StatusNotFound, nil, "文件不存在")
		return
	}
	defer file.Close()
	http.ServeContent(w, r, filepath.Base(filename), info.ModTime(), file)
}

func (a *App) resolveFilePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(a.configDir, "files", filepath.FromSlash(path))
}

func decodeBase64Param(value string) (string, error) {
	value = strings.ReplaceAll(value, " ", "+")
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	return string(decoded), err
}

func (a *App) allowedMediaPath(path string) bool {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil || !media.IsSupported(path) {
		return false
	}
	filesRoot, filesErr := filepath.Abs(filepath.Join(a.configDir, "files"))
	if filesErr == nil && isWithinPath(filesRoot, path) {
		return true
	}
	for _, item := range a.subscriptions.Items() {
		resolved, resolveErr := a.subscriptions.DownloadPath(item)
		if resolveErr != nil {
			continue
		}
		root := resolved["downloadPath"].(string)
		relative, relErr := filepath.Rel(root, path)
		if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isWithinPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sourceError(err error) string {
	if err == nil {
		return "外部服务失败"
	}
	return err.Error()
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (a *App) protected(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.config.Snapshot()
		if appconfig.Bool(cfg, "innerIP") && !a.auth.InnerIP(r) {
			writeResult(w, http.StatusForbidden, nil, "禁止公网访问")
			return
		}
		if !a.auth.Authorized(r) {
			writeResult(w, http.StatusForbidden, nil, "登录已失效")
			return
		}
		handler(w, r)
	})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeResult(w http.ResponseWriter, code int, data any, message string) {
	payload := map[string]any{"code": code, "message": message, "t": time.Now().UnixMilli()}
	if data != nil {
		payload["data"] = data
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Spring's ResultException is serialized as a JSON result while retaining
	// its result code; the browser consumes code rather than transport status.
	_ = json.NewEncoder(w).Encode(payload)
}

func ConfigDirFromEnv() string {
	if dir := strings.TrimSpace(os.Getenv("CONFIG")); dir != "" {
		return dir
	}
	if info, err := os.Stat("config"); err == nil && info.IsDir() {
		return "config"
	}
	return "config"
}
