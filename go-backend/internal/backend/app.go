// Package backend is the Go business application mounted behind the migration
// Gateway. Its handlers deliberately speak the existing Result envelope.
package backend

import (
	"bytes"
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
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/auth"
	"github.com/shijie152/ani-rss/go-backend/internal/collection"
	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/httpclient"
	"github.com/shijie152/ani-rss/go-backend/internal/mcp"
	"github.com/shijie152/ani-rss/go-backend/internal/media"
	"github.com/shijie152/ani-rss/go-backend/internal/metadata"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/notification"
	"github.com/shijie152/ani-rss/go-backend/internal/ownership"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
	"github.com/shijie152/ani-rss/go-backend/internal/update"
)

type Options struct {
	ConfigDir        string
	Version          string
	UpdateEndpoint   string
	Shutdown         func(bool)
	Logger           *slog.Logger
	OwnershipDomains []string
	MCPEnabled       bool
	SwaggerEnabled   bool
}

type App struct {
	store         store.Store
	history       store.HistoryStore
	tasks         store.TaskStore
	config        *appconfig.Manager
	auth          *auth.Authenticator
	ownership     *ownership.Manager
	subscriptions *subscription.Service
	configDir     string
	logger        *slog.Logger
	notifications *notification.Dispatcher
	mu            sync.RWMutex
	refreshMu     sync.Mutex
	backgroundWG  sync.WaitGroup
	ownedDomains  []string
	version       string
	logBuffer     *logBuffer
	logFile       *os.File
	mcp           http.Handler
	swagger       bool
	updater       *update.Client
	shutdown      func(bool)
}

func New(options Options) (*App, error) {
	applicationStore, err := store.NewSQLiteStore(options.ConfigDir)
	if err != nil {
		return nil, err
	}
	manager, err := appconfig.NewManager(applicationStore)
	if err != nil {
		return nil, err
	}
	items, err := applicationStore.LoadSubscriptions()
	if err != nil {
		return nil, err
	}
	if err := subscription.ValidateItems(items); err != nil {
		return nil, fmt.Errorf("订阅数据校验失败: %w", err)
	}
	locks, err := ownership.NewManager(filepath.Join(applicationStore.Directory(), "locks"))
	if err != nil {
		return nil, err
	}
	ownedDomains := make([]string, 0, len(options.OwnershipDomains))
	for _, domain := range options.OwnershipDomains {
		if err := locks.Acquire(domain, "go"); err != nil {
			locks.Close()
			_ = applicationStore.Close()
			return nil, err
		}
		ownedDomains = append(ownedDomains, domain)
	}
	logger := options.Logger
	logs := newLogBuffer(appconfig.Int(manager.Snapshot(), "logsMax"))
	logDirectory := filepath.Join(applicationStore.Directory(), "logs")
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		locks.Close()
		_ = applicationStore.Close()
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDirectory, "ani-rss.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		locks.Close()
		_ = applicationStore.Close()
		return nil, fmt.Errorf("open log file: %w", err)
	}
	fileHandler := slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug})
	var handlers slog.Handler = &teeHandler{first: logs, second: fileHandler}
	if logger != nil {
		handlers = &teeHandler{first: handlers, second: logger.Handler()}
	}
	logger = slog.New(handlers)
	if options.Version != "" {
		if err := manager.Update(model.Config{"version": options.Version}); err != nil {
			_ = logFile.Close()
			locks.Close()
			_ = applicationStore.Close()
			return nil, err
		}
	}
	app := &App{store: applicationStore, history: applicationStore, tasks: applicationStore, config: manager, auth: auth.New(manager), ownership: locks, subscriptions: subscription.NewService(applicationStore, manager, items), configDir: applicationStore.Directory(), logger: logger, logBuffer: logs, logFile: logFile, version: options.Version, notifications: notification.New(manager, applicationStore.Directory(), nil, logger), ownedDomains: ownedDomains, swagger: options.SwaggerEnabled, shutdown: options.Shutdown}
	app.logger.Info("Go backend initialized")
	app.subscriptions.ConfigureSideEffects(func(ctx context.Context) (subscription.TaskManager, error) {
		coordinator, factoryErr := app.newCoordinator()
		if factoryErr != nil {
			return nil, factoryErr
		}
		return coordinator.QB, nil
	}, app.runBackground, app.configDir)
	app.subscriptions.ConfigureAddSideEffect(func(item model.Ani) {
		app.runBackground(func() {
			app.refreshMu.Lock()
			defer app.refreshMu.Unlock()
			coordinator, coordinatorErr := app.newCoordinator()
			if coordinatorErr != nil {
				app.logger.Warn("initial subscription refresh setup failed", "subscription", item.ID, "error", coordinatorErr)
				return
			}
			if item.Enable {
				// Java checks downloader connectivity before fetching the RSS
				// feed. This also keeps an enabled subscription with an
				// intentionally unconfigured downloader from blocking startup or
				// the response lifecycle on a feed timeout.
				if loginErr := coordinator.QB.Login(context.Background()); loginErr != nil {
					app.logger.Warn("initial subscription refresh skipped", "subscription", item.ID, "error", loginErr)
					return
				}
				if _, refreshErr := coordinator.Refresh(context.Background(), item); refreshErr != nil {
					app.logger.Warn("initial subscription refresh failed", "subscription", item.ID, "error", refreshErr)
				}
				return
			}
			resources, previewErr := coordinator.Preview(context.Background(), item)
			if previewErr != nil {
				app.logger.Warn("initial subscription preview failed", "subscription", item.ID, "error", previewErr)
				return
			}
			if updateErr := app.subscriptions.UpdateCurrentEpisode(item.ID, resources); updateErr != nil {
				app.logger.Warn("initial subscription progress update failed", "subscription", item.ID, "error", updateErr)
			}
		})
	})
	if err := app.repairLoadedSubscriptions(items); err != nil {
		app.Close()
		return nil, err
	}
	if strings.TrimSpace(options.UpdateEndpoint) != "" && options.Version != "" && options.Version != "dev" {
		app.updater = &update.Client{Endpoint: options.UpdateEndpoint, Version: options.Version, Token: appconfig.String(manager.Snapshot(), "githubToken")}
	}
	if options.MCPEnabled {
		app.mcp = mcp.New(mcp.Config{Version: options.Version, Authorize: app.auth.APIKey, Tools: app.mcpTools()})
	}
	return app, nil
}

func (a *App) Config() *appconfig.Manager { return a.config }
func (a *App) Store() store.Store         { return a.store }
func (a *App) Auth() *auth.Authenticator  { return a.auth }

// OwnedDomains returns the domains successfully claimed by this process.
func (a *App) OwnedDomains() []string {
	return append([]string(nil), a.ownedDomains...)
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

func (a *App) Close() {
	a.backgroundWG.Wait()
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
	a.ownership.Close()
	if closer, ok := a.store.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

// runBackground mirrors the Java controllers' fire-and-forget executor while
// keeping App.Close safe during tests and orderly shutdown. The request
// context is intentionally not captured: Java work continues after the HTTP
// response has been written.
func (a *App) runBackground(fn func()) {
	a.backgroundWG.Add(1)
	go func() {
		defer a.backgroundWG.Done()
		fn()
	}()
}

// repairLoadedSubscriptions mirrors AniUtil.load's compatibility pass. The
// persisted store may contain records written by older Java versions with
// omitted fields, so repair happens before the first list/API response and is
// persisted once for future restarts.
func (a *App) repairLoadedSubscriptions(items []model.Ani) error {
	if len(items) == 0 {
		return nil
	}
	cfg := a.config.Snapshot()
	changed := false
	now := time.Now()
	for index := range items {
		item := &items[index]
		if !item.FieldPresent("releaseDate") || strings.TrimSpace(item.ReleaseDate) == "" {
			date := now
			if item.Year > 0 && item.Month >= 1 && item.Month <= 12 && item.Date >= 1 && item.Date <= 31 {
				date = time.Date(item.Year, time.Month(item.Month), item.Date, 0, 0, 0, 0, time.Local)
			}
			item.ReleaseDate = date.Format("2006-01-02")
			changed = true
		}
		if item.StandbyRSSList == nil {
			item.StandbyRSSList = []model.StandbyRSS{}
			changed = true
		}
		if item.Match == nil {
			item.Match = []string{}
			changed = true
		}
		if item.Exclude == nil {
			item.Exclude = append([]string(nil), defaultSubscriptionExclude...)
			changed = true
		}
		if item.NotDownload == nil {
			item.NotDownload = []float64{}
			changed = true
		}
		if item.CustomPriorityKeywords == nil {
			item.CustomPriorityKeywords = []string{}
			changed = true
		}
		if item.CustomTags == nil {
			item.CustomTags = []string{}
			changed = true
		}
		if item.TMDB == nil {
			item.TMDB = map[string]any{"id": "", "name": "", "originalName": "", "date": now.Format("2006-01-02 15:04:05")}
			changed = true
		}
		if !item.FieldPresent("enable") {
			item.Enable = true
			changed = true
		}
		if !item.FieldPresent("customDownloadPath") {
			item.CustomDownloadPath = false
			changed = true
		}
		if !item.FieldPresent("customDownloadPathTemplate") {
			item.CustomDownloadPathTemplate = ""
			changed = true
		}
		if !item.FieldPresent("globalExclude") {
			item.GlobalExclude = false
			changed = true
		}
		if !item.FieldPresent("customEpisode") {
			item.CustomEpisode = appconfig.Bool(cfg, "customEpisode")
			changed = true
		}
		if !item.FieldPresent("customEpisodeStr") {
			item.CustomEpisodeStr = appconfig.String(cfg, "customEpisodeStr")
			changed = true
		}
		if !item.FieldPresent("customEpisodeGroupIndex") {
			item.CustomEpisodeGroupIndex = appconfig.Int(cfg, "customEpisodeGroupIndex")
			changed = true
		}
		if !item.FieldPresent("omit") {
			item.Omit = true
			changed = true
		}
		if !item.FieldPresent("upload") {
			item.Upload = appconfig.Bool(cfg, "upload")
			changed = true
		}
		if !item.FieldPresent("procrastinating") {
			item.Procrastinating = true
			changed = true
		}
		if !item.FieldPresent("customRenameTemplateEnable") {
			item.CustomRenameTemplateEnable = false
			changed = true
		}
		if !item.FieldPresent("customRenameTemplate") {
			item.CustomRenameTemplate = appconfig.String(cfg, "renameTemplate")
			changed = true
		}
		if !item.FieldPresent("customPriorityKeywordsEnable") {
			item.CustomPriorityKeywordsEnable = false
			changed = true
		}
		if !item.FieldPresent("customUploadEnable") {
			item.CustomUploadEnable = false
			changed = true
		}
		if !item.FieldPresent("customUploadPathTarget") {
			item.CustomUploadPathTarget = ""
			changed = true
		}
		if !item.FieldPresent("message") {
			item.Message = true
			changed = true
		}
		if !item.FieldPresent("completed") {
			item.Completed = true
			changed = true
		}
		if !item.FieldPresent("customCompleted") {
			item.CustomCompleted = false
			changed = true
		}
		if !item.FieldPresent("customCompletedPathTemplate") {
			item.CustomCompletedPathTemplate = ""
			changed = true
		}
		if !item.FieldPresent("customTagsEnable") {
			item.CustomTagsEnable = false
			changed = true
		}
	}

	// Java repairs every cover, including old records whose remote image was
	// temporarily unavailable; RefreshCover supplies cover.png on failure.
	if service, err := a.mediaService(); err == nil {
		for index := range items {
			cover, _ := service.RefreshCover(context.Background(), items[index].Image, false)
			if cover != "" && items[index].Cover != cover {
				items[index].Cover = cover
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := a.store.SaveSubscriptions(items); err != nil {
		return fmt.Errorf("保存修补后的订阅失败: %w", err)
	}
	return a.subscriptions.Reload()
}

// RunSchedulers runs the scheduler domains owned by this Go process.
//
// The method blocks until ctx is cancelled and is intended to run in one
// goroutine from the command entrypoint.
func (a *App) RunSchedulers(ctx context.Context) {
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
	routes := []gateway.Route{
		runtime(http.MethodGet, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodPost, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodPut, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodDelete, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodPatch, "/api/ping", http.HandlerFunc(a.ping)),
		runtime(http.MethodOptions, "/api/ping", http.HandlerFunc(a.pingOptions)),
		runtime(http.MethodPost, "/api/login", http.HandlerFunc(a.login)),
		runtime(http.MethodPost, "/api/config", a.protected(a.configGet)),
		runtime(http.MethodPost, "/api/setConfig", a.protected(a.configSet)),
		runtime(http.MethodPost, "/api/testIpWhitelist", http.HandlerFunc(a.testIPWhitelist)),
		runtime(http.MethodGet, "/api/custom.js", http.HandlerFunc(a.customJS)),
		runtime(http.MethodGet, "/api/custom.css", http.HandlerFunc(a.customCSS)),
		runtime(http.MethodPost, "/api/testProxy", a.protected(a.testProxy)),
		runtime(http.MethodPost, "/api/logs", a.protected(a.logs)),
		runtime(http.MethodPost, "/api/clearLogs", a.protected(a.clearLogs)),
		runtime(http.MethodGet, "/api/downloadLogs", a.protected(a.downloadLogs)),
		runtime(http.MethodPost, "/api/clearCache", a.protected(a.clearCache)),
		runtime(http.MethodPost, "/api/trackersUpdate", a.protected(a.trackersUpdate)),
		runtime(http.MethodGet, "/api/exportConfig", a.protected(a.exportConfig)),
		runtime(http.MethodPost, "/api/importConfig", a.protected(a.importConfig)),
		runtime(http.MethodGet, "/api/proxyImage", a.protected(a.proxyImage)),
		runtime(http.MethodGet, "/api/calendar.ics", a.protected(a.calendar)),
		runtime(http.MethodPost, "/api/about", a.protected(a.about)),
		runtime(http.MethodPost, "/api/update", a.protected(a.update)),
		runtime(http.MethodPost, "/api/stop", a.protected(a.stop)),
		runtime(http.MethodPost, "/api/webui/upload", a.protected(a.webuiUpload)),
		runtime(http.MethodPost, "/api/webui/delete", a.protected(a.webuiDelete)),
		runtime(http.MethodPost, "/api/webui/getUpdate", a.protected(a.webuiGetUpdate)),
		runtime(http.MethodPost, "/api/webui/update", a.protected(a.webuiUpdate)),
		runtime(http.MethodPost, "/api/testNotification", a.protected(a.testNotification)),
		runtime(http.MethodPost, "/api/newNotification", a.protected(a.newNotification)),
		runtime(http.MethodPost, "/api/getTgUpdates", a.protected(a.getTgUpdates)),
		runtime(http.MethodPost, "/api/getEmbyViews", a.protected(a.getEmbyViews)),
		runtime(http.MethodPost, "/api/embyWebHook", a.protected(a.embyWebHook)),
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
		{Domain: "sources", Method: http.MethodPost, Path: "/api/rate", Handler: a.protected(a.rate)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/setRate", Handler: a.protected(a.setRate)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/meBgm", Handler: a.protected(a.meBGM)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/bgm/oauth/callback", Handler: a.protected(a.bgmOAuthCallback)},
		{Domain: "sources", Method: http.MethodPost, Path: "/api/rssToAni", Handler: a.protected(a.rssToAni)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/refreshAll", Handler: a.protected(a.refreshAll)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/refreshAni", Handler: a.protected(a.refreshAni)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/previewAni", Handler: a.protected(a.previewAni)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/deleteTorrent", Handler: a.protected(a.deleteTorrent)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/torrentsInfos", Handler: a.protected(a.torrentsInfos)},
		{Domain: "rss", Method: http.MethodPost, Path: "/api/downloadLoginTest", Handler: a.protected(a.downloadLoginTest)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/startCollection", Handler: a.protected(a.startCollection)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/previewCollection", Handler: a.protected(a.previewCollection)},
		{Domain: "media", Method: http.MethodPost, Path: "/api/getCollectionSubgroup", Handler: a.protected(a.getCollectionSubgroup)},
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
	if a.mcp != nil {
		routes = append(routes, runtime(http.MethodPost, "/api/mcp", a.mcp))
	}
	if a.swagger {
		routes = append(routes,
			runtime(http.MethodGet, "/v3/api-docs", http.HandlerFunc(a.openapi)),
			runtime(http.MethodGet, "/swagger-ui.html", http.HandlerFunc(a.swaggerRedirect)),
			runtime(http.MethodGet, "/swagger-ui/index.html", http.HandlerFunc(a.swaggerUI)),
		)
	}
	return routes
}

func (a *App) ping(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusOK, nil, "success")
}

func (a *App) pingOptions(w http.ResponseWriter, _ *http.Request) {
	// Spring handles the OPTIONS preflight before invoking the controller, so
	// the legacy endpoint returns an empty successful response.
	w.WriteHeader(http.StatusOK)
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
	w.Header().Set("Content-Type", "application/javascript;charset=utf-8")
	_, _ = io.WriteString(w, value)
}

func (a *App) customCSS(w http.ResponseWriter, _ *http.Request) {
	value := appconfig.String(a.config.Snapshot(), "customCss")
	if value == "" {
		value = "/* empty css */"
	}
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Content-Type", "text/css")
	_, _ = io.WriteString(w, value)
}

func (a *App) testProxy(w http.ResponseWriter, r *http.Request) {
	encodedURL, queryErr := requiredQuery(r, "url")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	encoded := strings.ReplaceAll(encodedURL, " ", "+")
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
	deleteFilesValue, queryErr := requiredQueryWithType(r, "deleteFiles", "Boolean")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	deleteFiles, err := strconv.ParseBool(deleteFilesValue)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "删除文件参数异常")
		return
	}
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
	valueText, queryErr := requiredQueryWithType(r, "value", "Boolean")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	value, err := strconv.ParseBool(valueText)
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
	forceText, queryErr := requiredQueryWithType(r, "force", "Boolean")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	force, err := strconv.ParseBool(forceText)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "强制参数异常")
		return
	}
	if len(ids) == 0 {
		writeResult(w, http.StatusInternalServerError, nil, "未选择订阅")
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
	a.runBackground(func() {
		if err := a.subscriptions.UpdateTotalEpisodes(context.Background(), force, ids, resolve); err != nil {
			a.logger.Warn("total episode update partially failed", "error", err)
		}
	})
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
	if strings.TrimSpace(item.Title) == "" {
		writeResult(w, http.StatusInternalServerError, nil, "订阅标题不能为空")
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
		BGMCoverURL:     "https://cache.wushuo.top/bgm/cover",
		Config:          cfg,
		HTTPClient:      client,
		Retries:         appconfig.Int(cfg, "downloadRetry"),
		Subscriptions:   a.subscriptions.Items,
	}), nil
}

func (a *App) mikan(w http.ResponseWriter, r *http.Request) {
	text, queryErr := requiredQuery(r, "text")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	var season model.Config
	if err := decodeJSON(r, &season); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "Mikan 参数格式异常: "+err.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result map[string]any
		result, err = client.Mikan(text, season)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) mikanGroup(w http.ResponseWriter, r *http.Request) {
	target, queryErr := requiredQuery(r, "url")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.MikanGroup(target)
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
	bgmID, queryErr := requiredQuery(r, "bgmId")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.AniBTGroup(bgmID)
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
	bgmID, queryErr := requiredQuery(r, "bgmId")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.AnimeGardenGroup(bgmID)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) searchBgm(w http.ResponseWriter, r *http.Request) {
	name, queryErr := requiredQuery(r, "name")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result []map[string]any
		result, err = client.SearchBangumi(name)
		if err == nil {
			writeResult(w, http.StatusOK, result, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) getAniBySubjectID(w http.ResponseWriter, r *http.Request) {
	id, queryErr := requiredQuery(r, "id")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	client, err := a.sourceClient()
	if err == nil {
		var result model.Ani
		result, err = client.SubscriptionFromSubject(id)
		if err == nil {
			a.enrichSubscriptionMetadata(r.Context(), &result)
			a.applySubscriptionDefaults(&result, true)
			if service, serviceErr := a.mediaService(); serviceErr == nil {
				cover, _ := service.RefreshCover(r.Context(), result.Image, false)
				result.Cover = cover
			}
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
	if strings.TrimSpace(input.URL) == "" {
		writeResult(w, http.StatusInternalServerError, nil, "RSS解析失败 RSS地址 不能为空")
		return
	}
	client, err := a.sourceClient()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	typeName := defaultString(input.Type, "mikan")
	var resolvedMikan model.Ani
	id := source.SubjectID(input.BGMURL)
	parsedURL, parseErr := url.Parse(input.URL)
	if parseErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, "RSS地址格式异常: "+parseErr.Error())
		return
	}
	switch typeName {
	case "mikan":
		// Java only loads the Mikan detail page when both optional fields are
		// absent. The normal single-add UI already supplies the linked BGM URL
		// and subgroup; batch-add supplies neither and therefore takes this
		// branch.
		if strings.TrimSpace(input.BGMURL) == "" && strings.TrimSpace(input.Subgroup) == "" {
			resolved, resolveErr := client.ResolveMikanSubscription(input.URL)
			if resolveErr == nil {
				input.BGMURL = resolved.BGMURL
				resolvedMikan = resolved
				if input.Subgroup == "" {
					input.Subgroup = resolved.Subgroup
				}
				id = source.SubjectID(input.BGMURL)
			} else {
				writeResult(w, http.StatusInternalServerError, nil, sourceError(resolveErr))
				return
			}
		}
	case "ani-bt":
		if values, exists := parsedURL.Query()["bgmId"]; exists && len(values) > 0 {
			input.BGMURL = "https://bgm.tv/subject/" + values[0]
		} else {
			input.BGMURL = ""
		}
		if strings.TrimSpace(input.Subgroup) == "" {
			if values, exists := parsedURL.Query()["groupSlug"]; exists && len(values) > 0 {
				input.Subgroup = values[0]
			}
		}
	case "anime-garden":
		if values, exists := parsedURL.Query()["subject"]; exists && len(values) > 0 {
			input.BGMURL = "https://bgm.tv/subject/" + values[0]
		} else {
			input.BGMURL = ""
		}
		if values, exists := parsedURL.Query()["fansub"]; exists && len(values) > 0 {
			input.Subgroup = values[0]
		}
	default:
		// Java's `other` branch uses only the BGM URL supplied in the body;
		// query parameters on the RSS URL are not inferred.
	}
	id = source.SubjectID(input.BGMURL)
	if id == "" {
		writeResult(w, http.StatusInternalServerError, nil, "bgmUrl 不能为空")
		return
	}
	item, err := client.SubscriptionFromSubject(id)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	item.URL, item.Type, item.Subgroup = input.URL, typeName, input.Subgroup
	if typeName == "mikan" && resolvedMikan.MikanTitle != "" {
		item.MikanTitle = resolvedMikan.MikanTitle
	}
	item.Enable = input.Enable == nil || *input.Enable
	a.enrichSubscriptionMetadata(r.Context(), &item)
	a.applySubscriptionDefaults(&item, false)
	a.applyRSSConversionDefaults(&item, typeName)
	if strings.TrimSpace(item.Subgroup) == "" {
		item.Subgroup = "未知字幕组"
	}
	if item.Subgroup == "未知字幕组" {
		if resources, fetchErr := a.rssConversionResources(r.Context(), item.URL, &item); fetchErr == nil {
			if subgroup := inferRSSSubgroup(resources); subgroup != "" {
				item.Subgroup = subgroup
			}
		}
	}
	if item.Subgroup == "" {
		item.Subgroup = "未知字幕组"
	}
	if appconfig.Bool(a.config.Snapshot(), "copyMasterToStandby") && appconfig.Bool(a.config.Snapshot(), "standbyRss") {
		item.StandbyRSSList = append(item.StandbyRSSList, model.StandbyRSS{Label: item.Subgroup, URL: strings.TrimSpace(item.URL), Offset: 0})
	}
	if service, serviceErr := a.mediaService(); serviceErr == nil {
		// RefreshCover returns Java's fallback path even when downloading the
		// remote image fails. Keep that path in the response so the edit form
		// never receives an empty cover field.
		cover, _ := service.RefreshCover(r.Context(), item.Image, false)
		item.Cover = cover
	}
	if !item.OVA && appconfig.Bool(a.config.Snapshot(), "offset") {
		if resources, fetchErr := a.rssConversionResources(r.Context(), item.URL, &item); fetchErr == nil {
			minimum := 0.0
			for _, resource := range resources {
				if resource.Episode <= 0 || resource.Episode != float64(int(resource.Episode)) {
					continue
				}
				if minimum == 0 || resource.Episode < minimum {
					minimum = resource.Episode
				}
			}
			if minimum > 0 {
				item.Offset = -(int(minimum) - 1)
				for index := range item.StandbyRSSList {
					item.StandbyRSSList[index].Offset = item.Offset
				}
			}
		}
	}
	writeResult(w, http.StatusOK, item, "success")
}

func (a *App) rssConversionResources(ctx context.Context, feedURL string, item *model.Ani) ([]model.Resource, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	body, err := rss.Fetch(ctx, client, feedURL, appconfig.Int(cfg, "downloadRetry"))
	if err != nil {
		return nil, err
	}
	resources, err := rss.Parse(body, item.Subgroup, feedURL)
	if err != nil {
		return nil, err
	}
	if item.Subgroup == "未知字幕组" {
		if subgroup := inferRSSSubgroup(resources); subgroup != "" {
			item.Subgroup = subgroup
			for index := range resources {
				resources[index].Subgroup = subgroup
			}
		}
	}
	options := rss.MatchOptions{
		GlobalExclude:    appconfig.Strings(cfg, "exclude"),
		DownloadNew:      item.DownloadNew,
		SkipHalf:         appconfig.Bool(cfg, "skip5"),
		DelayedMinutes:   appconfig.Int(cfg, "delayedDownload"),
		CustomEpisode:    item.CustomEpisode,
		CustomEpisodeRE:  item.CustomEpisodeStr,
		CustomEpisodeIdx: item.CustomEpisodeGroupIndex,
		Coexist:          appconfig.Bool(cfg, "coexist"),
	}
	if item.CustomPriorityKeywordsEnable {
		options.PriorityKeywords = item.CustomPriorityKeywords
	} else if appconfig.Bool(cfg, "priorityKeywordsEnable") {
		options.PriorityKeywords = appconfig.Strings(cfg, "priorityKeywords")
	}
	return rss.Match(resources, *item, options), nil
}

func inferRSSSubgroup(resources []model.Resource) string {
	pattern := regexp.MustCompile(`^\[([^]]+)]`)
	for _, resource := range resources {
		name := strings.TrimSpace(resource.Title)
		if match := pattern.FindStringSubmatch(name); len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			return strings.TrimSpace(match[1])
		}
		name = filepath.Base(name)
		if match := pattern.FindStringSubmatch(name); len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

var defaultSubscriptionExclude = []string{"720[Pp]", `\d-\d`, "合集", "特别篇"}

// applySubscriptionDefaults is the Go equivalent of Java's createAni().
// Source conversion endpoints must return the complete editable object: the
// Vue form binds directly to these fields and treats missing values as
// undefined, which makes a seemingly successful conversion look incomplete.
func (a *App) applySubscriptionDefaults(item *model.Ani, customDownloadPath bool) {
	if item == nil {
		return
	}
	cfg := a.config.Snapshot()
	if item.ID == "" {
		item.ID = fmt.Sprintf("ani-%d", time.Now().UnixNano())
	}
	if item.ReleaseDate == "" {
		item.ReleaseDate = time.Now().Format("2006-01-02")
	}
	if item.Season < 1 {
		item.Season = 1
	}
	if item.StandbyRSSList == nil {
		item.StandbyRSSList = []model.StandbyRSS{}
	}
	if item.Match == nil {
		item.Match = []string{}
	}
	// createAni() starts with its fixed filters. RSS conversion imports the
	// global filters in applyRSSConversionDefaults, after BgmUtil.toAni(); a
	// direct Bangumi conversion must retain these fixed defaults.
	item.Exclude = append([]string(nil), defaultSubscriptionExclude...)
	item.GlobalExclude = false
	item.CustomEpisode = appconfig.Bool(cfg, "customEpisode")
	item.CustomEpisodeStr = appconfig.String(cfg, "customEpisodeStr")
	item.CustomEpisodeGroupIndex = appconfig.Int(cfg, "customEpisodeGroupIndex")
	item.Omit = true
	item.DownloadNew = false
	if item.NotDownload == nil {
		item.NotDownload = []float64{}
	}
	if item.TMDB == nil {
		item.TMDB = map[string]any{"id": "", "name": "", "originalName": "", "date": time.Now().Format("2006-01-02 15:04:05")}
	}
	item.Upload = appconfig.Bool(cfg, "upload")
	item.Procrastinating = !item.OVA
	item.CustomRenameTemplateEnable = false
	item.CustomRenameTemplate = appconfig.String(cfg, "renameTemplate")
	item.CustomPriorityKeywordsEnable = false
	if item.CustomPriorityKeywords == nil {
		item.CustomPriorityKeywords = []string{}
	}
	item.CustomUploadEnable = false
	item.CustomUploadPathTarget = ""
	item.Message = true
	item.Completed = true
	item.CustomCompleted = false
	item.CustomCompletedPathTemplate = appconfig.String(cfg, "completedPathTemplate")
	item.CustomTagsEnable = false
	if item.CustomTags == nil {
		item.CustomTags = []string{}
	}
	item.CustomDownloadPath = customDownloadPath
	if path, err := a.subscriptions.DownloadPath(*item); err == nil {
		item.CustomDownloadPathTemplate = path["downloadPath"].(string)
	}
}

// applyRSSConversionDefaults contains the fields that AniUtil.getAni applies
// only after the RSS source has been converted. They are deliberately kept
// separate from createAni defaults because /getAniBySubjectId calls the latter
// directly and Java leaves downloadNew/globalExclude at their fixed values in
// that path.
func (a *App) applyRSSConversionDefaults(item *model.Ani, typeName string) {
	if item == nil {
		return
	}
	cfg := a.config.Snapshot()
	if appconfig.Bool(cfg, "importExclude") {
		item.Exclude = appendUniqueStrings(appconfig.Strings(cfg, "exclude"), item.Exclude)
	}
	item.GlobalExclude = appconfig.Bool(cfg, "enabledExclude")
	item.DownloadNew = appconfig.Bool(cfg, "downloadNew")
	item.Type = typeName
}

func appendUniqueStrings(first, second []string) []string {
	result := make([]string, 0, len(first)+len(second))
	seen := make(map[string]struct{}, len(first)+len(second))
	for _, values := range [][]string{first, second} {
		for _, value := range values {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func (a *App) enrichSubscriptionMetadata(ctx context.Context, item *model.Ani) {
	if item == nil {
		return
	}
	// Bangumi remains the authoritative fallback when TMDB is unavailable.
	// Java formats this title even when the optional TMDB lookup fails, so a
	// transient TMDB outage must not leave the editable form with an unclean
	// or otherwise incomplete title.
	bangumiFallback := a.bangumiDisplayTitle(*item, model.Metadata{})
	cfg := a.config.Snapshot()
	// Search with the raw Bangumi title. Formatting the title first can append
	// the configured year (or TMDB id), which is a display concern and may make
	// an otherwise valid TMDB search miss. Java's BgmUtil.toAni() resolves TMDB
	// immediately after setting the raw Bangumi title, then formats the result.
	searchTitle := strings.TrimSpace(item.Title)
	if searchTitle == "" {
		searchTitle = "无标题"
	}
	metadataClient, err := a.metadataClient()
	if err != nil {
		item.Title = bangumiFallback
		return
	}
	value, raw, err := metadataClient.SearchTMDB(ctx, searchTitle, item.OVA)
	if err != nil {
		item.Title = bangumiFallback
		return
	}
	item.TMDB = raw
	item.TheMovieDBName = media.FinalTitle(value, a.config)
	// Java always resolves and stores TMDB, but the `tmdb` switch controls
	// whether its title replaces the Bangumi title. Other subscription fields
	// remain sourced from Bangumi in both modes.
	if appconfig.Bool(cfg, "tmdb") && item.TheMovieDBName != "" {
		item.Title = item.TheMovieDBName
	} else {
		item.Title = a.bangumiDisplayTitle(*item, value)
	}
}

func (a *App) bangumiDisplayTitle(item model.Ani, tmdbValue model.Metadata) string {
	title := cleanDisplayTitle(item.Title)
	if title == "" {
		title = "无标题"
	}
	cfg := a.config.Snapshot()
	if appconfig.Bool(cfg, "titleYear") {
		year := tmdbValue.Year
		if year == 0 {
			year = dateYear(item.ReleaseDate)
		}
		if year > 0 {
			title = regexp.MustCompile(`\s*\((?:19|20)\d{2}\)\s*$`).ReplaceAllString(title, "")
			title = fmt.Sprintf("%s (%d)", title, year)
		}
	}
	if appconfig.Bool(cfg, "tmdbId") && tmdbValue.ID != "" {
		if appconfig.Bool(cfg, "tmdbIdPlexMode") {
			title += " {tmdb-" + tmdbValue.ID + "}"
		} else {
			title += " [tmdbid=" + tmdbValue.ID + "]"
		}
	}
	return title
}

// cleanDisplayTitle mirrors RenameUtil.getName for titles originating from
// Bangumi. It is intentionally applied only to display/path-safe titles; RSS
// matching continues to use the original resource title.
func cleanDisplayTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "1/2", "½")
	value = strings.NewReplacer(
		"/", " ", "\\", " ", ":", "：", "?", "？", "|", "｜",
		"*", " ", "<", " ", ">", " ", "\"", " ",
	).Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func dateYear(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil {
		return 0
	}
	return year
}

func (a *App) newCoordinator() (*rss.Coordinator, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	adapter, err := downloader.New(cfg, client)
	if err != nil {
		return nil, err
	}
	return &rss.Coordinator{Config: a.config, Subscriptions: a.subscriptions, History: a.history, HTTPClient: client, ConfigDir: a.configDir, Retry: appconfig.Int(cfg, "downloadRetry"), QB: adapter,
		Notify: func(ctx context.Context, ani model.Ani, resource *model.Resource, status, text string) error {
			return a.notifications.Dispatch(ctx, notification.Event{Ani: ani, Resource: resource, Status: status, Text: text})
		}}, nil
}

func (a *App) refreshAll(w http.ResponseWriter, r *http.Request) {
	a.runBackground(func() {
		if err := a.refreshAllSubscriptions(context.Background()); err != nil {
			a.logger.Warn("RSS refresh partially failed", "error", err)
		}
	})
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
	a.runBackground(func() {
		if err := a.refreshSubscription(context.Background(), selected); err != nil {
			a.logger.Warn("RSS refresh failed", "subscription", selected.ID, "error", err)
		}
	})
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
	if strings.TrimSpace(item.URL) == "" {
		writeResult(w, http.StatusInternalServerError, nil, "RSS地址不能为空")
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
	id, queryErr := requiredQuery(r, "id")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	hash, queryErr := requiredQuery(r, "hash")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	var selected model.Ani
	found := false
	for _, item := range a.subscriptions.Items() {
		if item.ID == id {
			selected, found = item, true
			break
		}
	}
	if !found {
		writeResult(w, http.StatusInternalServerError, nil, "此订阅不存在")
		return
	}
	hashes := map[string]bool{}
	for _, value := range strings.Split(hash, ",") {
		if value = strings.TrimSpace(value); value != "" {
			hashes[strings.ToLower(value)] = true
		}
	}
	resources, err := a.history.LoadResources()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	kept := make([]model.Resource, 0, len(resources))
	for _, value := range resources {
		if value.AniID == id && hashes[strings.ToLower(value.InfoHash)] {
			continue
		}
		kept = append(kept, value)
	}
	if err := a.history.SaveResources(kept); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	if err := rss.DeleteResourceCache(a.configDir, selected, hashes); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "删除完成")
}

func (a *App) torrentsInfos(w http.ResponseWriter, r *http.Request) {
	cfg := a.config.Snapshot()
	toolType := defaultString(appconfig.String(cfg, "downloadToolType"), "qBittorrent")
	if strings.EqualFold(toolType, "qBittorrent") &&
		(strings.TrimSpace(appconfig.String(cfg, "downloadToolHost")) == "" || strings.TrimSpace(appconfig.String(cfg, "downloadToolPassword")) == "") {
		// The legacy qBittorrent adapter treats an intentionally unconfigured
		// downloader as an empty task list. Keep that distinct from a configured
		// downloader that is unavailable or rejects authentication.
		writeResult(w, http.StatusOK, []model.Torrent{}, "success")
		return
	}
	coordinator, err := a.newCoordinator()
	if err == nil {
		if err = coordinator.QB.Login(r.Context()); err == nil {
			var result []model.Torrent
			result, err = coordinator.QB.Torrents(r.Context())
			if err == nil {
				if a.tasks != nil {
					if saveErr := a.tasks.SaveTasks(result); saveErr != nil {
						a.logger.Warn("download task snapshot could not be persisted", "error", saveErr)
					}
				}
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
		var adapter downloader.Adapter
		adapter, err = downloader.New(cfg, client)
		if err == nil {
			err = adapter.Login(r.Context())
		}
	}
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "登录失败")
		return
	}
	writeResult(w, http.StatusOK, nil, "登录成功")
}

func (a *App) collectionService() (*collection.Service, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, err
	}
	adapter, err := downloader.New(cfg, client)
	if err != nil {
		return nil, err
	}
	qb, ok := adapter.(*downloader.QBittorrent)
	if !ok {
		return nil, errors.New("合集下载暂时只支持 qBittorrent")
	}
	return &collection.Service{Config: a.config, Download: qb}, nil
}

func (a *App) startCollection(w http.ResponseWriter, r *http.Request) {
	var info model.CollectionInfo
	if err := decodeJSON(r, &info); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "合集参数格式异常: "+err.Error())
		return
	}
	service, err := a.collectionService()
	if err == nil {
		err = service.Start(r.Context(), info)
	}
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	writeResult(w, http.StatusOK, nil, "已经开始下载合集")
}

func (a *App) previewCollection(w http.ResponseWriter, r *http.Request) {
	var info model.CollectionInfo
	if err := decodeJSON(r, &info); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "合集参数格式异常: "+err.Error())
		return
	}
	service := &collection.Service{Config: a.config}
	items, err := service.Preview(info)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	writeResult(w, http.StatusOK, items, "success")
}

func (a *App) getCollectionSubgroup(w http.ResponseWriter, r *http.Request) {
	var info model.CollectionInfo
	if err := decodeJSON(r, &info); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "合集参数格式异常: "+err.Error())
		return
	}
	service := &collection.Service{Config: a.config}
	subgroup, err := service.Subgroup(info)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
		return
	}
	writeResult(w, http.StatusOK, subgroup, "success")
}

func (a *App) newNotification(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusOK, notification.NewConfig(), "success")
}

func (a *App) testNotification(w http.ResponseWriter, r *http.Request) {
	var cfg map[string]any
	if err := decodeJSON(r, &cfg); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "通知配置格式异常: "+err.Error())
		return
	}
	event := notification.Event{Ani: model.Ani{ID: "notification-test", Title: "ANI-RSS 测试", JPTitle: "テスト", Season: 1, Message: true, TMDB: map[string]any{"id": "292970"}}, Status: notification.DownloadStart, Text: "test"}
	if err := a.notifications.Test(r.Context(), cfg, event); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "测试成功")
}

func (a *App) getTgUpdates(w http.ResponseWriter, r *http.Request) {
	var cfg map[string]any
	if err := decodeJSON(r, &cfg); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	result, err := a.notifications.TelegramUpdates(r.Context(), cfg)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, result, "success")
}

func (a *App) getEmbyViews(w http.ResponseWriter, r *http.Request) {
	var cfg map[string]any
	if err := decodeJSON(r, &cfg); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	host, token := strings.TrimRight(appconfig.String(cfg, "embyHost"), "/"), appconfig.String(cfg, "embyApiKey")
	if host == "" {
		writeResult(w, http.StatusInternalServerError, nil, "embyHost 为空")
		return
	}
	if token == "" {
		writeResult(w, http.StatusInternalServerError, nil, "embyApiKey 为空")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, host+"/Library/MediaFolders", nil)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	request.Header.Set("X-Emby-Token", token)
	response, err := a.notifications.Client.Do(request)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeResult(w, http.StatusInternalServerError, nil, fmt.Sprintf("Emby HTTP %d", response.StatusCode))
		return
	}
	var body struct {
		Items []map[string]any `json:"Items"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&body); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, body.Items, "success")
}

func (a *App) embyWebHook(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := decodeJSON(r, &payload); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "Emby Webhook 格式异常: "+err.Error())
		return
	}
	// Java treats a webhook as a no-op when Bangumi integration is disabled.
	// Emby sends test/health payloads even in that default configuration, so
	// they must not be rejected for lacking an event field.
	if appconfig.String(a.config.Snapshot(), "bgmToken") == "" {
		writeResult(w, http.StatusOK, nil, "success")
		return
	}
	event := strings.ToLower(appconfig.String(payload, "event"))
	if event == "" {
		writeResult(w, http.StatusInternalServerError, nil, "Emby Webhook 缺少 event")
		return
	}
	if err := a.processEmbyWebhook(r.Context(), payload); err != nil {
		// Java treats webhook processing as best-effort and returns success to
		// Emby. Log the diagnostic while keeping the external webhook stable.
		a.logger.Warn("Emby webhook processing failed", "error", err)
	}
	writeResult(w, http.StatusOK, nil, "success")
}

func (a *App) processEmbyWebhook(ctx context.Context, payload map[string]any) error {
	cfg := a.config.Snapshot()
	token := appconfig.String(cfg, "bgmToken")
	if token == "" {
		return nil
	}
	event := strings.ToLower(appconfig.String(payload, "event"))
	if event == "system.webhooktest" || event == "system.notificationtest" {
		return nil
	}
	item, _ := payload["Item"].(map[string]any)
	if item == nil {
		item, _ = payload["item"].(map[string]any)
	}
	fileName, seriesName := appconfig.String(item, "FileName"), appconfig.String(item, "SeriesName")
	if fileName == "" {
		fileName = appconfig.String(item, "fileName")
	}
	if seriesName == "" {
		seriesName = appconfig.String(item, "seriesName")
	}
	match := regexp.MustCompile(`(?i)s(\d{1,3})[ ._-]*e(\d+(?:\.5)?)`).FindStringSubmatch(fileName)
	if len(match) < 3 {
		return nil
	}
	season, _ := strconv.Atoi(match[1])
	episode, _ := strconv.ParseFloat(match[2], 64)
	if season < 1 || episode != float64(int(episode)) {
		return nil
	}
	playback, _ := payload["PlaybackInfo"].(map[string]any)
	if playback == nil {
		playback, _ = payload["playbackInfo"].(map[string]any)
	}
	status := -1
	switch event {
	case "item.markunplayed":
		status = 0
	case "item.markplayed":
		status = 2
	case "playback.stop":
		if played, ok := playback["PlayedToCompletion"].(bool); ok && played {
			status = 2
		}
		if played, ok := playback["playedToCompletion"].(bool); ok && played {
			status = 2
		}
	}
	if status < 0 {
		return nil
	}
	var selected model.Ani
	found := false
	for _, candidate := range a.subscriptions.Items() {
		if candidate.Season != season || candidate.BGMURL == "" {
			continue
		}
		if candidate.Title == seriesName || candidate.TheMovieDBName == seriesName {
			selected, found = candidate, true
			break
		}
	}
	if !found {
		return nil
	}
	subjectID := source.SubjectID(selected.BGMURL)
	if subjectID == "" {
		return nil
	}
	client := a.notifications.Client
	base := strings.TrimRight(appconfig.String(cfg, "bgmApi"), "/")
	if base == "" {
		base = "https://api.bgm.tv"
	}
	form := url.Values{"subject_id": []string{subjectID}, "type": []string{"0"}, "limit": []string{"1000"}, "offset": []string{"0"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v0/episodes", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Bangumi episodes HTTP %d", response.StatusCode)
	}
	var episodes struct {
		Data []struct {
			ID   string  `json:"id"`
			Ep   float64 `json:"ep"`
			Sort float64 `json:"sort"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&episodes); err != nil {
		return err
	}
	episodeID := ""
	for _, candidate := range episodes.Data {
		if candidate.Ep == episode || candidate.Sort == episode {
			episodeID = candidate.ID
			if candidate.Ep == episode {
				break
			}
		}
	}
	if episodeID == "" {
		return nil
	}
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v0/users/-/collections/-/episodes/"+url.PathEscape(episodeID), nil)
	if err != nil {
		return err
	}
	get.Header.Set("Authorization", "Bearer "+token)
	currentResponse, err := client.Do(get)
	if err != nil {
		return err
	}
	defer currentResponse.Body.Close()
	var current struct {
		Type int `json:"type"`
	}
	if currentResponse.StatusCode >= 200 && currentResponse.StatusCode < 300 {
		_ = json.NewDecoder(io.LimitReader(currentResponse.Body, 1<<20)).Decode(&current)
	}
	if current.Type == status {
		return nil
	}
	body, _ := json.Marshal(map[string]int{"type": status})
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/v0/users/-/collections/-/episodes/"+url.PathEscape(episodeID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	put.Header.Set("Authorization", "Bearer "+token)
	put.Header.Set("Content-Type", "application/json")
	updated, err := client.Do(put)
	if err != nil {
		return err
	}
	defer updated.Body.Close()
	if updated.StatusCode < 200 || updated.StatusCode >= 300 {
		return fmt.Errorf("Bangumi episode update HTTP %d", updated.StatusCode)
	}
	return nil
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
	service.Notify = func(ctx context.Context, item model.Ani, path, status string) error {
		return a.notifications.Dispatch(ctx, notification.Event{Ani: item, Status: status, Text: "下载完成: " + item.Title, Path: path})
	}
	return service, nil
}

func (a *App) scrape(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "订阅格式异常: "+err.Error())
		return
	}
	forceText, queryErr := requiredQueryWithType(r, "force", "Boolean")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	force, err := strconv.ParseBool(forceText)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "强制参数异常")
		return
	}
	service, err := a.mediaService()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	a.runBackground(func() {
		result, scrapeErr := service.Scrape(context.Background(), &item, force)
		if result.Ani.ID != "" && scrapeErr == nil {
			if saveErr := a.subscriptions.Set(result.Ani); saveErr != nil {
				scrapeErr = saveErr
			}
		}
		if scrapeErr != nil {
			a.logger.Warn("media scrape failed", "title", item.Title, "error", scrapeErr)
		}
	})
	writeResult(w, http.StatusOK, nil, "已开始刮削 "+item.Title)
}

func (a *App) batchScrape(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if err := decodeJSON(r, &ids); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "未选择订阅")
		return
	}
	forceText, queryErr := requiredQueryWithType(r, "force", "Boolean")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	force, err := strconv.ParseBool(forceText)
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "强制参数异常")
		return
	}
	if len(ids) == 0 {
		writeResult(w, http.StatusInternalServerError, nil, "未选择订阅")
		return
	}
	service, err := a.mediaService()
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	selectedItems := make([]model.Ani, 0, len(ids))
	for _, item := range a.subscriptions.Items() {
		if !selected[item.ID] {
			continue
		}
		selectedItems = append(selectedItems, item)
	}
	a.runBackground(func() {
		for index := range selectedItems {
			item := &selectedItems[index]
			result, scrapeErr := service.Scrape(context.Background(), item, force)
			if scrapeErr == nil && result.Ani.ID != "" {
				scrapeErr = a.subscriptions.Set(result.Ani)
			}
			if scrapeErr != nil {
				a.logger.Warn("media scrape failed", "title", item.Title, "error", scrapeErr)
			}
		}
	})
	writeResult(w, http.StatusOK, nil, fmt.Sprintf("已开始刮削%d个订阅", len(selectedItems)))
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
		if err == nil {
			writeResult(w, http.StatusOK, cover, "success")
			return
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if isRequestTooLarge(err) {
			writeResult(w, http.StatusRequestEntityTooLarge, nil, "文件过大")
		} else {
			writeResult(w, http.StatusInternalServerError, nil, "Content-Type is not supported")
		}
		return
	}
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
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(header.Filename)), ".")
	relative := filepath.ToSlash(filepath.Join(string(name[0]), name+"."+ext))
	target := filepath.Join(a.configDir, "files", relative)
	if err := writeAtomic(target, data); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, relative, "上传完成")
}

func (a *App) uploadAndRead(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		if isRequestTooLarge(err) {
			writeResult(w, http.StatusRequestEntityTooLarge, nil, "文件过大")
		} else {
			writeResult(w, http.StatusInternalServerError, nil, "Content-Type is not supported")
		}
		return
	}
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
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if isRequestTooLarge(err) {
			writeResult(w, http.StatusRequestEntityTooLarge, nil, "文件过大")
		} else {
			writeResult(w, http.StatusInternalServerError, nil, "Content-Type is not supported")
		}
		return
	}
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
	if strings.TrimSpace(input.TMDBID) == "" && strings.TrimSpace(input.Title) == "" {
		writeResult(w, http.StatusInternalServerError, nil, "TmdbId 或 标题 不能为空")
		return
	}
	client, err := a.metadataClient()
	if err == nil {
		var value model.Metadata
		var raw map[string]any
		value, raw, err = client.LookupTMDB(r.Context(), input.Title, input.TMDBID, input.OVA)
		if err == nil {
			name := media.FinalTitle(value, a.config)
			if name == "" {
				writeResult(w, http.StatusInternalServerError, nil, "获取 TMDB 失败")
				return
			}
			writeResult(w, http.StatusOK, map[string]any{"tmdb": raw, "themoviedbName": name}, "获取 TMDB 成功")
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
	if strings.TrimSpace(id) == "" {
		writeResult(w, http.StatusInternalServerError, nil, "tmdb is null")
		return
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
	encodedFilename, queryErr := requiredQuery(r, "filename")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	filename, err := decodeBase64Param(encodedFilename)
	if err == nil {
		filename = a.resolveFilePath(filename)
		// The legacy endpoint is specifically for MKV's embedded subtitle
		// tracks. Sidecar subtitles are assembled by playList and must not be
		// duplicated here. Java also treats a missing extension and non-MKV
		// paths as a successful empty result.
		ext := strings.ToLower(filepath.Ext(filename))
		if ext == "" || ext != ".mkv" {
			writeResult(w, http.StatusOK, []model.SubtitleInfo{}, "success")
			return
		}
		if _, statErr := os.Stat(filename); statErr == nil && media.IsVideo(filename) && a.allowedMediaPath(filename) {
			// Sidecar subtitles are deliberately excluded here. Java exposes
			// them from playList; this endpoint is only the embedded-track
			// lookup used by PlayStartView.
			embedded, embeddedErr := media.EmbeddedSubtitles(filename)
			if embeddedErr == nil {
				writeResult(w, http.StatusOK, embedded, "success")
				return
			}
			// Java's EBML reader returns an empty successful result when the
			// file header is not readable. Preserve that behavior for a
			// present MKV instead of turning a corrupt media file into a
			// page-level error.
			writeResult(w, http.StatusOK, []model.SubtitleInfo{}, "success")
			return
		} else {
			err = errors.New("视频文件不存在")
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, err.Error())
}

func (a *App) file(w http.ResponseWriter, r *http.Request) {
	encodedFilename, queryErr := requiredQuery(r, "filename")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	filename, err := decodeBase64Param(encodedFilename)
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
	file, openErr := os.Open(filename)
	if openErr != nil {
		writeResult(w, http.StatusNotFound, nil, "文件不存在")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Disposition", `inline; filename="`+url.PathEscape(filepath.Base(filename))+`"`)
	w.Header().Set("Content-Type", contentType)
	if strings.HasPrefix(contentType, "video/") {
		if start, end, ok, rangeErr := javaRange(r.Header.Get("Range"), info.Size()); rangeErr != nil {
			writeResult(w, http.StatusInternalServerError, nil, "Range 参数异常")
			return
		} else if ok {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size()))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			if _, seekErr := file.Seek(start, io.SeekStart); seekErr == nil {
				_, _ = io.CopyN(w, file, end-start+1)
			}
			return
		}
	} else if info.Size() <= 3*1024*1024 {
		// Match the Java file endpoint: small non-video assets are safe to cache
		// for a month, while large assets remain uncached by default.
		w.Header().Set("Cache-Control", "public, max-age=2592000")
	}
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	_, _ = io.Copy(w, file)
}

// javaRange parses the deliberately small Range dialect implemented by the
// legacy FileController. In particular, a suffix range such as bytes=-5 is
// interpreted as bytes 0-5 there (rather than as the last five bytes), and a
// trailing dash leaves the default end at EOF.
func javaRange(value string, size int64) (start, end int64, present bool, err error) {
	if strings.TrimSpace(value) == "" || !strings.HasPrefix(value, "bytes=") {
		return 0, 0, false, nil
	}
	if size < 0 {
		return 0, 0, false, errors.New("文件大小异常")
	}
	start, end = 0, size-1
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		start, err = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			return 0, 0, false, err
		}
	}
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		end, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return 0, 0, false, err
		}
	}
	if start < 0 || end < 0 || end < start {
		return 0, 0, false, errors.New("Range 范围异常")
	}
	return start, end, true, nil
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
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	filesRoot, filesErr := filepath.Abs(filepath.Join(a.configDir, "files"))
	filesRoot, rootErr := filepath.EvalSymlinks(filesRoot)
	if filesErr == nil && rootErr == nil && isWithinPath(filesRoot, canonical) {
		return true
	}
	for _, item := range a.subscriptions.Items() {
		resolved, resolveErr := a.subscriptions.DownloadPath(item)
		if resolveErr != nil {
			continue
		}
		root, ok := resolved["downloadPath"].(string)
		if !ok {
			continue
		}
		root, rootErr = filepath.EvalSymlinks(root)
		if rootErr != nil {
			continue
		}
		relative, relErr := filepath.Rel(root, canonical)
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

func requiredQuery(r *http.Request, name string) (string, error) {
	return requiredQueryWithType(r, name, "String")
}

func requiredQueryWithType(r *http.Request, name, javaType string) (string, error) {
	values, ok := r.URL.Query()[name]
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("Required request parameter '%s' for method parameter type %s is not present", name, javaType)
	}
	return values[0], nil
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
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	// Spring's required @RequestBody rejects both an empty body and the JSON
	// literal null before the controller method is invoked. encoding/json
	// otherwise accepts null into a pointer/map/struct target, which would let
	// some Go handlers incorrectly take their successful empty-input path.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("request body is required")
	}
	bodyDecoder := json.NewDecoder(bytes.NewReader(raw))
	bodyDecoder.UseNumber()
	return bodyDecoder.Decode(target)
}

func writeResult(w http.ResponseWriter, code int, data any, message string) {
	payload := map[string]any{"code": code, "message": message, "t": time.Now().UnixMilli()}
	// Gson does not serialize null fields by default. This matters for the
	// Result<Void> responses used by most command endpoints, whose legacy JSON
	// envelope has no data key at all.
	if !isNilResultData(data) {
		payload["data"] = data
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	// Spring's ResultException is serialized as a JSON result while retaining
	// its result code; the browser consumes code rather than transport status.
	_ = json.NewEncoder(w).Encode(payload)
}

func isNilResultData(data any) bool {
	if data == nil {
		return true
	}
	value := reflect.ValueOf(data)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
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
