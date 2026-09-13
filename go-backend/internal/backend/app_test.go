package backend_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestRuntimeRoutesUseExistingResultContractAndProtectConfig(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	h := gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions"}})
	server := httptest.NewServer(h)
	defer server.Close()
	response, err := http.Get(server.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ping = %d", response.StatusCode)
	}
	response, err = http.Post(server.URL+"/api/config", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var denied map[string]any
	_ = json.NewDecoder(response.Body).Decode(&denied)
	response.Body.Close()
	if denied["code"] != float64(http.StatusForbidden) {
		t.Fatalf("denied = %#v", denied)
	}
	response, err = http.Post(server.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"21232f297a57a5a743894a0e4a801fc3"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login map[string]any
	_ = json.NewDecoder(response.Body).Decode(&login)
	response.Body.Close()
	if login["code"] != float64(http.StatusOK) {
		t.Fatalf("login = %#v", login)
	}
}

func TestNotificationAndEmbyRoutesPreserveUIContracts(t *testing.T) {
	var updated atomic.Int32
	var refreshed atomic.Int32
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/Library/MediaFolders":
			_, _ = io.WriteString(w, `{"Items":[{"Id":"library-1","Name":"Anime"}]}`)
		case "/v0/episodes":
			_, _ = io.WriteString(w, `{"data":[{"id":"episode-1","ep":1,"sort":1}]}`)
		case "/v0/users/-/collections/-/episodes/episode-1":
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `{"type":0}`)
			} else {
				updated.Add(1)
				_, _ = io.WriteString(w, `{"type":2}`)
			}
		case "/emby/Items/library-1/Refresh":
			refreshed.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer bgm.Close()
	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Config().Update(model.Config{"bgmApi": bgm.URL, "bgmToken": "bgm-token"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions"}}))
	defer server.Close()
	token := login(t, server.URL)
	newConfig := callJSON(t, server.URL+"/api/newNotification", token, nil)
	if newConfig["code"] != float64(http.StatusOK) || newConfig["data"] == nil {
		t.Fatalf("new notification = %#v", newConfig)
	}
	added := callJSON(t, server.URL+"/api/addAni", token, model.Ani{ID: "demo", Title: "Demo", Season: 1, BGMURL: "https://bgm.tv/subject/42", URL: "https://example.test/rss", Enable: true})
	if added["code"] != float64(http.StatusOK) {
		t.Fatalf("add notification webhook subscription = %#v", added)
	}
	views := callJSON(t, server.URL+"/api/getEmbyViews", token, map[string]any{"embyHost": bgm.URL, "embyApiKey": "emby-token"})
	if views["code"] != float64(http.StatusOK) || len(views["data"].([]any)) != 1 {
		t.Fatalf("Emby views = %#v", views)
	}
	webhook := callJSON(t, server.URL+"/api/embyWebHook", token, map[string]any{"event": "item.markplayed", "Item": map[string]any{"SeriesName": "Demo", "FileName": "Demo S01E01.mkv"}})
	if webhook["code"] != float64(http.StatusOK) {
		t.Fatalf("Emby webhook = %#v", webhook)
	}
	// The webhook work is intentionally queued like Java's single-threaded
	// executor; wait briefly for the BGM update before asserting the side effect.
	deadline := time.Now().Add(time.Second)
	for updated.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if updated.Load() != 1 {
		t.Fatalf("matched subscription did not update BGM: %d", updated.Load())
	}
	if refreshed.Load() != 0 {
		t.Fatalf("webhook unexpectedly refreshed Emby: %d", refreshed.Load())
	}
}

func TestSubscriptionRoutesPersistThroughRestart(t *testing.T) {
	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions"}}))
	unauthorized := callJSON(t, server.URL+"/api/listAni", "", nil)
	if unauthorized["code"] != float64(http.StatusForbidden) {
		t.Fatalf("unauthorized subscription list = %#v", unauthorized)
	}
	token := login(t, server.URL)
	item := model.Ani{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 1, Enable: true}
	invalid := callJSON(t, server.URL+"/api/addAni", token, model.Ani{ID: "invalid"})
	if invalid["code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("invalid subscription = %#v", invalid)
	}
	response := callJSON(t, server.URL+"/api/addAni", token, item)
	if response["code"] != float64(http.StatusOK) {
		t.Fatalf("add = %#v", response)
	}
	response = callJSON(t, server.URL+"/api/listAni", token, map[string]any{})
	if response["code"] != float64(http.StatusOK) {
		t.Fatalf("list = %#v", response)
	}
	list := response["data"].(map[string]any)
	if list["total"] != float64(1) {
		t.Fatalf("list total = %#v", list)
	}
	server.Close()
	app.Close()

	app, err = backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server = httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions"}}))
	defer server.Close()
	token = login(t, server.URL)
	response = callJSON(t, server.URL+"/api/listAni", token, map[string]any{})
	if response["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("restart lost subscription: %#v", response)
	}
}

func TestSubscriptionHTTPRoundTripsRulesAndRejectsEmptyImport(t *testing.T) {
	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions"}}))
	t.Cleanup(server.Close)
	t.Cleanup(app.Close)
	token := login(t, server.URL)
	item := model.Ani{
		ID: "rules", Title: "Rules", URL: "https://example.test/rules", Season: 2, Enable: true,
		StandbyRSSList: []model.StandbyRSS{{Label: "Backup", URL: "https://example.test/backup", Offset: 1}},
		Subgroup:       "Group", Offset: 2, Match: []string{"1080p"}, Exclude: []string{"720p"},
		GlobalExclude: true, CustomEpisode: true, CustomEpisodeStr: `(.*?)(E\\d+)`, CustomEpisodeGroupIndex: 2,
		DownloadNew: true, NotDownload: []float64{3}, CustomPriorityKeywordsEnable: true, CustomPriorityKeywords: []string{"HEVC"},
		CustomDownloadPath: true, CustomDownloadPathTemplate: filepath.Join(dir, "library", "${title}"),
		CustomRenameTemplateEnable: true, CustomRenameTemplate: "${title} ${episode}",
		CustomCompleted: true, CustomCompletedPathTemplate: filepath.Join(dir, "completed", "${title}"),
		CustomTagsEnable: true, CustomTags: []string{"tag"},
	}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add rules = %#v", response)
	}
	listed := callJSON(t, server.URL+"/api/listAni", token, nil)
	weeks := listed["data"].(map[string]any)["weekList"].([]any)
	var raw map[string]any
	for _, week := range weeks {
		for _, candidate := range week.(map[string]any)["items"].([]any) {
			value := candidate.(map[string]any)
			if value["id"] == item.ID {
				raw = value
			}
		}
	}
	if raw == nil || raw["offset"] != float64(2) || raw["subgroup"] != "Group" || raw["downloadNew"] != true {
		t.Fatalf("rules not visible in list: %#v", raw)
	}
	if got := raw["standbyRssList"].([]any)[0].(map[string]any); got["label"] != "Backup" || got["offset"] != float64(1) {
		t.Fatalf("standby rule = %#v", got)
	}
	if response := callJSON(t, server.URL+"/api/importAni", token, map[string]any{"aniList": []model.Ani{}}); response["code"] != float64(http.StatusInternalServerError) || response["message"] == "" {
		t.Fatalf("empty import = %#v", response)
	}
}

func TestHTTPConfigAuthAndBangumiEpisodeUpdateContract(t *testing.T) {
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"name":"Demo","name_cn":"示例","eps":12}`)
	}))
	defer bgm.Close()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()

	bad := callJSON(t, server.URL+"/api/login", "", model.Login{Username: "admin", Password: "bad"})
	if bad["code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("invalid login = %#v", bad)
	}
	token1 := login(t, server.URL)
	token2 := login(t, server.URL)
	if got := callJSON(t, server.URL+"/api/config", token1, nil)["code"]; got != float64(http.StatusForbidden) {
		t.Fatalf("multi-login token was not revoked: %#v", got)
	}
	config := callJSON(t, server.URL+"/api/config", token2, nil)
	if config["code"] != float64(http.StatusOK) {
		t.Fatalf("config = %#v", config)
	}
	public := config["data"].(map[string]any)
	loginConfig := public["login"].(map[string]any)
	if loginConfig["password"] != "" || public["jwtKey"] != "" {
		t.Fatalf("secrets leaked in public config: %#v", public)
	}
	invalidConfig := callJSON(t, server.URL+"/api/setConfig", token2, model.Config{"proxy": true, "proxyHost": "", "proxyPort": 8080})
	if invalidConfig["code"] != float64(http.StatusInternalServerError) || invalidConfig["message"] == "" {
		t.Fatalf("invalid config contract = %#v", invalidConfig)
	}
	set := callJSON(t, server.URL+"/api/setConfig", token2, model.Config{"bgmApi": bgm.URL + "/", "rssTimeout": 2})
	if set["code"] != float64(http.StatusOK) {
		t.Fatalf("set config = %#v", set)
	}
	item := model.Ani{ID: "episode-update", Title: "示例", URL: "https://example.test/rss", BGMURL: "https://bgm.tv/subject/42", Season: 1, Enable: true}
	if got := callJSON(t, server.URL+"/api/addAni", token2, item)["code"]; got != float64(http.StatusOK) {
		t.Fatalf("add = %#v", got)
	}
	updated := callJSON(t, server.URL+"/api/updateTotalEpisodeNumber?force=false", token2, []string{item.ID})
	if updated["code"] != float64(http.StatusOK) {
		t.Fatalf("update total = %#v", updated)
	}
	listed := callJSON(t, server.URL+"/api/listAni", token2, nil)
	weeks := listed["data"].(map[string]any)["weekList"].([]any)
	found := false
	for _, rawWeek := range weeks {
		for _, rawItem := range rawWeek.(map[string]any)["items"].([]any) {
			if rawItem.(map[string]any)["id"] == item.ID {
				found = rawItem.(map[string]any)["totalEpisodeNumber"] == float64(12)
			}
		}
	}
	if !found {
		t.Fatalf("Bangumi eps was not persisted: %#v", listed)
	}
	if response := callJSON(t, server.URL+"/api/setConfig", token2, model.Config{"mikanHost": "http://127.0.0.1:1", "rssTimeout": 1}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set failing source = %#v", response)
	}
	failedSource := callJSON(t, server.URL+"/api/mikan?text=demo", token2, nil)
	if failedSource["code"] != float64(http.StatusInternalServerError) || failedSource["message"] == "" {
		t.Fatalf("external failure contract = %#v", failedSource)
	}
}

func TestHTTPSourceConversionCanCreateVisibleSubscription(t *testing.T) {
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"season":1,"images":{"large":"https://img.test/demo.jpg"}}`)
	}))
	defer bgm.Close()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"bgmApi": bgm.URL}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set BGM config = %#v", response)
	}
	converted := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{"url": "https://example.test/feed.xml", "bgmUrl": "https://bgm.tv/subject/42", "subgroup": "Group"})
	if converted["code"] != float64(http.StatusOK) {
		t.Fatalf("rss conversion = %#v", converted)
	}
	item, ok := converted["data"].(map[string]any)
	if !ok || item["title"] != "Demo CN" || item["totalEpisodeNumber"] != float64(12) {
		t.Fatalf("converted item = %#v", converted["data"])
	}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add converted subscription = %#v", response)
	}
	listed := callJSON(t, server.URL+"/api/listAni", token, nil)
	if listed["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("converted subscription not visible = %#v", listed)
	}
}

func TestBackendRejectsMalformedPersistedSubscriptionData(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ani.v2.json"), []byte(`[{"id":"missing-title","url":"https://example.test/rss"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.New(backend.Options{ConfigDir: dir}); err == nil || !strings.Contains(err.Error(), "订阅数据校验失败") {
		t.Fatalf("malformed startup data error = %v", err)
	}
}

func TestBackendRejectsDuplicatePersistedSubscriptions(t *testing.T) {
	dir := t.TempDir()
	data := `[{"id":"one","title":"Demo","url":"https://example.test/one","season":1},{"id":"two","title":"Demo","url":"https://example.test/two","season":1}]`
	if err := os.WriteFile(filepath.Join(dir, "ani.v2.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.New(backend.Options{ConfigDir: dir}); err == nil || !strings.Contains(err.Error(), "标题和季度重复") {
		t.Fatalf("duplicate startup data error = %v", err)
	}
}

func TestHTTPRefreshDrivesRSSToQBittorrentCompletionChain(t *testing.T) {
	var added atomic.Int32
	var addedPath atomic.Value
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v4")
		case "/api/v2/torrents/info":
			if added.Load() == 0 {
				_, _ = io.WriteString(w, "[]")
				return
			}
			path, _ := addedPath.Load().(string)
			_, _ = io.WriteString(w, `[{"hash":"chain","name":"Demo S01E01","state":"stoppedUP","progress":1,"size":100,"amount_left":0,"save_path":"`+path+`","category":"ani-rss","tags":"ani-rss,Group"}]`)
		case "/api/v2/torrents/add":
			if err := r.ParseForm(); err != nil {
				t.Errorf("add form: %v", err)
			}
			addedPath.Store(r.Form.Get("savepath"))
			added.Add(1)
			_, _ = io.WriteString(w, "Ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<rss><channel><item><title>[Group] Demo E01</title><guid>chain-guid</guid><description>summary</description><enclosure url="magnet:?xt=urn:btih:CHAIN" length="100"/></item></channel></rss>`)
	}))
	defer feed.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "rss"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "rss"}}))
	defer server.Close()
	token := login(t, server.URL)
	path := filepath.Join(t.TempDir(), "${title}")
	config := model.Config{"downloadToolHost": qb.URL, "downloadToolPassword": "qbt_test", "downloadRetry": 1, "rssTimeout": 2, "downloadPathTemplate": path}
	if response := callJSON(t, server.URL+"/api/setConfig", token, config); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set chain config = %#v", response)
	}
	item := model.Ani{ID: "chain", Title: "Demo", URL: feed.URL, Subgroup: "Group", Season: 1, Enable: true}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add chain subscription = %#v", response)
	}
	if response := callJSON(t, server.URL+"/api/refreshAni", token, map[string]any{"id": item.ID}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("refresh chain = %#v", response)
	}
	if added.Load() != 1 {
		t.Fatalf("add count after refresh = %d", added.Load())
	}
	listed := callJSON(t, server.URL+"/api/listAni", token, nil)
	if listed["code"] != float64(http.StatusOK) {
		t.Fatalf("list after refresh = %#v", listed)
	}
	weeks := listed["data"].(map[string]any)["weekList"].([]any)
	if len(weeks) == 0 || len(weeks[0].(map[string]any)["items"].([]any)) == 0 {
		t.Fatalf("subscription disappeared after refresh: %#v", listed)
	}
	progress := weeks[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	if progress["currentEpisodeNumber"] != float64(1) {
		t.Fatalf("current episode after refresh = %#v", progress["currentEpisodeNumber"])
	}
	status := callJSON(t, server.URL+"/api/torrentsInfos", token, nil)
	if status["code"] != float64(http.StatusOK) {
		t.Fatalf("torrent status = %#v", status)
	}
	tasks := status["data"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["state"] != "stoppedUP" || tasks[0].(map[string]any)["progress"] != float64(100) {
		t.Fatalf("completed task = %#v", status)
	}
	if response := callJSON(t, server.URL+"/api/refreshAll", token, nil); response["code"] != float64(http.StatusOK) {
		t.Fatalf("refresh all = %#v", response)
	}
	if added.Load() != 1 {
		t.Fatalf("refreshAll duplicated task: %d", added.Load())
	}
}

func TestHTTPMediaRoutesUseMetadataAndFilesystemContract(t *testing.T) {
	bgmImageURL := ""
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/tv/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo Show","original_name":"Demo JP","overview":"overview","first_air_date":"2024-01-01","number_of_episodes":1,"vote_average":8.5,"poster_path":"/poster.jpg"}`)
		case "/3/tv/42/season/1":
			_, _ = io.WriteString(w, `{"episodes":[{"episode_number":1,"name":"Pilot","overview":"pilot","air_date":"2024-01-01","still_path":"/still.jpg"}]}`)
		case "/v0/subjects/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"images":{"large":"`+bgmImageURL+`"}}`)
		default:
			if strings.HasPrefix(r.URL.Path, "/t/p/original/") || r.URL.Path == "/cover.png" || r.URL.Path == "/bgm.jpg" {
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = io.WriteString(w, "image")
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer metadataServer.Close()

	bgmImageURL = metadataServer.URL + "/bgm.jpg"
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<rss><channel><item><title>[Group] Demo E01</title><enclosure url="magnet:?xt=urn:btih:PREVIEW" length="100"/></item></channel></rss>`)
	}))
	defer feed.Close()

	root := t.TempDir()
	mediaRoot := filepath.Join(root, "library", "Demo")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(mediaRoot, "[Group] Demo - 01 [1080p].mkv")
	if err := os.WriteFile(video, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	// PlaybackList intentionally excludes tiny files; a sparse file exercises
	// that public contract without making the test expensive.
	if err := os.Truncate(video, 20*1024*1024); err != nil {
		t.Fatal(err)
	}

	app, err := backend.New(backend.Options{ConfigDir: root, OwnershipDomains: []string{"runtime", "subscriptions", "sources", "rss", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources", "rss", "media"}}))
	defer server.Close()
	token := login(t, server.URL)
	config := model.Config{
		"tmdbApi":              metadataServer.URL,
		"tmdbImage":            metadataServer.URL,
		"tmdbApiKey":           "test",
		"bgmApi":               metadataServer.URL,
		"downloadPathTemplate": filepath.Join(root, "library", "${title}"),
	}
	if response := callJSON(t, server.URL+"/api/setConfig", token, config); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set media config = %#v", response)
	}
	item := model.Ani{ID: "media", Title: "Demo", URL: feed.URL, BGMURL: "https://bgm.tv/subject/42", Subgroup: "Group", Season: 1, Enable: true, TMDB: map[string]any{"id": "42"}, CustomDownloadPath: true, CustomDownloadPathTemplate: mediaRoot}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add media subscription = %#v", response)
	}

	if response := callJSON(t, server.URL+"/api/scrape?force=true", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("scrape = %#v", response)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, "[Group] Demo S01E01.mkv")); err != nil {
		entries, _ := os.ReadDir(mediaRoot)
		t.Fatalf("scrape did not rename media: %v entries=%v", err, entries)
	}
	if response := callJSON(t, server.URL+"/api/batchScrape?force=false", token, []string{item.ID}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("batch scrape = %#v", response)
	}
	playlist := callJSON(t, server.URL+"/api/playList", token, item)
	if playlist["code"] != float64(http.StatusOK) || len(playlist["data"].([]any)) != 1 {
		t.Fatalf("playlist = %#v", playlist)
	}
	coverItem := item
	coverItem.Image = metadataServer.URL + "/cover.png"
	if response := callJSON(t, server.URL+"/api/refreshCover", token, coverItem); response["code"] != float64(http.StatusOK) {
		t.Fatalf("refresh cover = %#v", response)
	}
	if response := callJSON(t, server.URL+"/api/updateTotalEpisodeNumber?force=true", token, []string{item.ID}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("update total episodes = %#v", response)
	}
	preview := callJSON(t, server.URL+"/api/previewAni", token, item)
	if preview["code"] != float64(http.StatusOK) || len(preview["data"].(map[string]any)["items"].([]any)) != 1 {
		t.Fatalf("preview = %#v", preview)
	}
}

func TestHTTPMediaRejectsMissingMetadataAndFileTraversal(t *testing.T) {
	metadataServer := httptest.NewServer(http.NotFoundHandler())
	defer metadataServer.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "files", "safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "files", "safe", "video.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.mkv")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := backend.New(backend.Options{ConfigDir: root, OwnershipDomains: []string{"runtime", "subscriptions", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "media"}}))
	defer server.Close()
	token := login(t, server.URL)

	allowed := get(t, server.URL+"/api/file?filename="+base64.RawStdEncoding.EncodeToString([]byte("safe/video.mkv")), token)
	if allowed.StatusCode != http.StatusOK {
		allowed.Body.Close()
		t.Fatalf("allowed media file status = %d", allowed.StatusCode)
	}
	allowedBody, _ := io.ReadAll(allowed.Body)
	allowed.Body.Close()
	if string(allowedBody) != "video" {
		t.Fatalf("allowed media file body = %q", allowedBody)
	}

	traversal := get(t, server.URL+"/api/file?filename="+base64.RawStdEncoding.EncodeToString([]byte("../outside.mkv")), token)
	body, _ := io.ReadAll(traversal.Body)
	traversal.Body.Close()
	var denied map[string]any
	if err := json.Unmarshal(body, &denied); err != nil || denied["code"] != float64(http.StatusForbidden) {
		t.Fatalf("traversal response = status %d body=%q", traversal.StatusCode, body)
	}

	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"tmdbApi": metadataServer.URL, "tmdbImage": metadataServer.URL, "tmdbApiKey": "test", "downloadPathTemplate": filepath.Join(root, "library", "${title}")}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set missing-metadata config = %#v", response)
	}
	missing := callJSON(t, server.URL+"/api/scrape?force=true", token, model.Ani{ID: "missing", Title: "Missing", URL: "https://example.test/rss", Season: 1, TMDB: map[string]any{"id": "404"}})
	if missing["code"] != float64(http.StatusInternalServerError) || missing["message"] == "" {
		t.Fatalf("missing metadata response = %#v", missing)
	}
}

func TestCollectionRoutesAndMediaStreamingAreUICompatible(t *testing.T) {
	var renamed atomic.Int32
	var reprioritized atomic.Int32
	var started atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v4")
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("collection multipart = %v", err)
			}
			if r.FormValue("paused") != "true" || r.FormValue("rename") == "" {
				t.Errorf("collection add fields = %#v", r.MultipartForm.Value)
			}
			_, _ = io.WriteString(w, "Ok")
		case "/api/v2/torrents/files":
			_, _ = io.WriteString(w, `[{"index":0,"name":"Demo Collection/[Group] Demo E01.mkv","size":100,"priority":1},{"index":1,"name":"Demo Collection/[Group] Demo E01.chs.ass","size":20,"priority":1},{"index":2,"name":"Demo Collection/Extras.txt","size":5,"priority":1}]`)
		case "/api/v2/torrents/renameFile":
			renamed.Add(1)
		case "/api/v2/torrents/filePrio":
			reprioritized.Add(1)
		case "/api/v2/torrents/start":
			started.Add(1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()

	root := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: root, OwnershipDomains: []string{"runtime", "subscriptions", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "media"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"downloadToolHost": qb.URL, "downloadToolPassword": "api-key"}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set downloader config = %#v", response)
	}
	info := map[string]any{"filename": "collection.torrent", "torrent": base64.StdEncoding.EncodeToString(testCollectionTorrent()), "ani": model.Ani{Title: "Demo", Season: 1, Subgroup: "Group", Offset: 1, CustomDownloadPathTemplate: filepath.Join(root, "media")}}
	preview := callJSON(t, server.URL+"/api/previewCollection", token, info)
	if preview["code"] != float64(http.StatusOK) || len(preview["data"].([]any)) != 2 {
		t.Fatalf("collection preview = %#v", preview)
	}
	group := callJSON(t, server.URL+"/api/getCollectionSubgroup", token, info)
	if group["code"] != float64(http.StatusOK) || group["data"] != "Group" {
		t.Fatalf("collection subgroup = %#v", group)
	}
	startedResponse := callJSON(t, server.URL+"/api/startCollection", token, info)
	if startedResponse["code"] != float64(http.StatusOK) || renamed.Load() != 2 || reprioritized.Load() != 1 || started.Load() != 1 {
		t.Fatalf("collection start=%#v rename=%d priority=%d start=%d", startedResponse, renamed.Load(), reprioritized.Load(), started.Load())
	}

	mediaDir := filepath.Join(root, "library")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(mediaDir, "Demo S01E01.mkv")
	if err := os.WriteFile(video, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "Demo S01E01.chs.ass"), []byte("subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(root, "files", "cover.png")
	if err := os.MkdirAll(filepath.Dir(image), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.mkv")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(mediaDir, "linked.mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(mediaDir, "notes.txt")
	if err := os.WriteFile(invalid, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := model.Ani{ID: "media", Title: "Media", URL: "https://example.test/media", Season: 1, CustomDownloadPath: true, CustomDownloadPathTemplate: mediaDir}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add media root = %#v", response)
	}
	rangeResponse := getWithHeader(t, server.URL+"/api/file?filename="+base64.RawStdEncoding.EncodeToString([]byte(video)), token, "Range", "bytes=2-5")
	rangeBody, _ := io.ReadAll(rangeResponse.Body)
	rangeResponse.Body.Close()
	if rangeResponse.StatusCode != http.StatusPartialContent || string(rangeBody) != "2345" || rangeResponse.Header.Get("Content-Range") != "bytes 2-5/10" || rangeResponse.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range response status=%d headers=%v body=%q", rangeResponse.StatusCode, rangeResponse.Header, rangeBody)
	}
	subtitles := callJSON(t, server.URL+"/api/getSubtitles?filename="+base64.RawStdEncoding.EncodeToString([]byte(video)), token, nil)
	if subtitles["code"] != float64(http.StatusOK) || len(subtitles["data"].([]any)) != 1 {
		t.Fatalf("subtitle response = %#v", subtitles)
	}
	imageResponse := get(t, server.URL+"/api/file?filename="+base64.RawStdEncoding.EncodeToString([]byte("cover.png")), token)
	imageResponse.Body.Close()
	if imageResponse.StatusCode != http.StatusOK || imageResponse.Header.Get("Cache-Control") != "public, max-age=2592000" || !strings.HasPrefix(imageResponse.Header.Get("Content-Type"), "image/png") {
		t.Fatalf("image response status=%d headers=%v", imageResponse.StatusCode, imageResponse.Header)
	}
	unauthorized := get(t, server.URL+"/api/file?filename="+base64.RawStdEncoding.EncodeToString([]byte(video)), "")
	var unauthorizedPayload map[string]any
	if err := json.NewDecoder(unauthorized.Body).Decode(&unauthorizedPayload); err != nil {
		unauthorized.Body.Close()
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorizedPayload["code"] != float64(http.StatusForbidden) {
		t.Fatalf("unauthorized media response = %#v", unauthorizedPayload)
	}
	for _, filename := range []string{"linked.mkv", "notes.txt"} {
		response := get(t, server.URL+"/api/file?filename="+base64.RawStdEncoding.EncodeToString([]byte(filepath.Join(mediaDir, filename))), token)
		var payload map[string]any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if payload["code"] != float64(http.StatusForbidden) {
			t.Fatalf("unsafe/illegal media %s response = %#v", filename, payload)
		}
	}
}

func getWithHeader(t *testing.T, target, token, key, value string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", token)
	request.Header.Set(key, value)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func testCollectionTorrent() []byte {
	info := testBDict(map[string][]byte{
		"files": testBList(
			testBDict(map[string][]byte{"length": testBInt(100), "path": testBList(testBString("[Group] Demo E01.mkv"))}),
			testBDict(map[string][]byte{"length": testBInt(20), "path": testBList(testBString("[Group] Demo E01.chs.ass"))}),
			testBDict(map[string][]byte{"length": testBInt(5), "path": testBList(testBString("Extras.txt"))}),
		),
		"name": testBString("Demo Collection"),
	})
	return testBDict(map[string][]byte{"info": info})
}

func testBString(value string) []byte { return []byte(strconv.Itoa(len(value)) + ":" + value) }
func testBInt(value int64) []byte     { return []byte("i" + strconv.FormatInt(value, 10) + "e") }
func testBList(values ...[]byte) []byte {
	result := []byte("l")
	for _, value := range values {
		result = append(result, value...)
	}
	return append(result, 'e')
}
func testBDict(values map[string][]byte) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []byte("d")
	for _, key := range keys {
		result = append(result, testBString(key)...)
		result = append(result, values[key]...)
	}
	return append(result, 'e')
}

func TestRunSchedulersRefreshesRSSOnlyWhenGoOwnsTheDomain(t *testing.T) {
	var added atomic.Int32
	refreshed := make(chan struct{}, 1)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v4")
		case "/api/v2/torrents/info":
			_, _ = io.WriteString(w, "[]")
		case "/api/v2/torrents/add":
			added.Add(1)
			select {
			case refreshed <- struct{}{}:
			default:
			}
			_, _ = io.WriteString(w, "Ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<rss><channel><item><title>[Group] Scheduled E01</title><enclosure url="magnet:?xt=urn:btih:SCHEDULED" length="100"/></item></channel></rss>`)
	}))
	defer feed.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "rss"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Config().Update(model.Config{
		"downloadToolHost":     qb.URL,
		"downloadToolPassword": "qbt_test",
		"downloadRetry":        1,
		"rssTimeout":           2,
		"downloadPathTemplate": filepath.Join(t.TempDir(), "${title}"),
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "rss"}}))
	defer server.Close()
	token := login(t, server.URL)
	item := model.Ani{ID: "scheduled", Title: "Scheduled", URL: feed.URL, Subgroup: "Group", Season: 1, Enable: true}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add scheduled subscription = %#v", response)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.RunSchedulers(ctx)
		close(done)
	}()
	select {
	case <-refreshed:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("Go RSS scheduler did not refresh the subscription")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Go RSS scheduler did not stop")
	}
	if added.Load() != 1 {
		t.Fatalf("scheduled add count = %d", added.Load())
	}
}

func login(t *testing.T, baseURL string) string {
	t.Helper()
	response := callJSON(t, baseURL+"/api/login", "", model.Login{Username: "admin", Password: "21232f297a57a5a743894a0e4a801fc3"})
	if response["code"] != float64(http.StatusOK) {
		t.Fatalf("login = %#v", response)
	}
	return response["data"].(string)
}

func callJSON(t *testing.T, target, token string, body any) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("%s: %v", string(data), err)
	}
	return payload
}

func get(t *testing.T, target, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
