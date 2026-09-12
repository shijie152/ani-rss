package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/ownership"
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

func TestDifferentialPingAgainstJavaWhenConfigured(t *testing.T) {
	javaURL := strings.TrimRight(os.Getenv("ANI_RSS_JAVA_URL"), "/")
	if javaURL == "" {
		t.Skip("set ANI_RSS_JAVA_URL to run the Go/Java differential smoke test")
	}
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes()}))
	defer server.Close()
	goResult := callJSON(t, server.URL+"/api/ping", "", nil)
	javaResult := callJSON(t, javaURL+"/api/ping", "", nil)
	for _, key := range []string{"code", "message"} {
		if goResult[key] != javaResult[key] {
			t.Fatalf("differential ping %s: Go=%#v Java=%#v", key, goResult[key], javaResult[key])
		}
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

func TestStateOwnershipFallsBackToJavaForWriteRoutes(t *testing.T) {
	dir := t.TempDir()
	javaOwner, err := ownership.NewManager(filepath.Join(dir, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	if err := javaOwner.Acquire("state", "java"); err != nil {
		t.Fatal(err)
	}
	defer javaOwner.Close()

	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"state", "runtime", "subscriptions", "sources", "rss", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owned := app.OwnedDomains()
	for _, domain := range []string{"runtime", "subscriptions", "rss", "media"} {
		for _, actual := range owned {
			if actual == domain {
				t.Fatalf("state-conflicting domain %q remained Go-owned: %v", domain, owned)
			}
		}
	}
	if len(owned) != 1 || owned[0] != "sources" {
		t.Fatalf("read-only fallback domains = %v", owned)
	}
	javaFallback, err := ownership.NewManager(filepath.Join(dir, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	if err := javaFallback.Acquire("rss", "java"); err != nil {
		t.Fatalf("Go retained RSS lock after state conflict: %v", err)
	}
	javaFallback.Close()
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
