package backend_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestPingAcceptsJavaRequestMethodsAndOmitsNullData(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime"}}))
	defer server.Close()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		request, err := http.NewRequest(method, server.URL+"/api/ping", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		decodeErr := json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || payload["code"] != float64(http.StatusOK) {
			t.Fatalf("ping %s: status=%d decode=%v payload=%#v", method, response.StatusCode, decodeErr, payload)
		}
		if _, present := payload["data"]; present {
			t.Fatalf("ping %s unexpectedly contains null data: %#v", method, payload)
		}
	}

	request, err := http.NewRequest(http.MethodOptions, server.URL+"/api/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || readErr != nil || len(body) != 0 {
		t.Fatalf("ping OPTIONS: status=%d read=%v body=%q", response.StatusCode, readErr, body)
	}
}

func TestVoidResultErrorsAlsoOmitNullData(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime"}}))
	defer server.Close()

	response := callJSON(t, server.URL+"/api/login", "", model.Login{Username: "admin", Password: "wrong"})
	if _, present := response["data"]; present {
		t.Fatalf("login error unexpectedly contains null data: %#v", response)
	}
}

func TestTorrentsInfosReturnsEmptyListWhenQBittorrentIsUnconfigured(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "rss"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "rss"}}))
	defer server.Close()

	response := callJSON(t, server.URL+"/api/torrentsInfos", login(t, server.URL), map[string]any{})
	items, ok := response["data"].([]any)
	if response["code"] != float64(http.StatusOK) || !ok || len(items) != 0 {
		t.Fatalf("unconfigured torrent list = %#v", response)
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
	if _, present := newConfig["data"].(map[string]any)["embyHost"]; present {
		t.Fatalf("new notification unexpectedly contains Java-null field: %#v", newConfig["data"])
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

func TestEmbyWebhookWithoutBangumiTokenIsASuccessfulNoop(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime"}}))
	defer server.Close()

	response := callJSON(t, server.URL+"/api/embyWebHook", login(t, server.URL), map[string]any{})
	if response["code"] != float64(http.StatusOK) || response["message"] != "success" {
		t.Fatalf("disabled Emby webhook = %#v", response)
	}
	if _, present := response["data"]; present {
		t.Fatalf("disabled Emby webhook unexpectedly contains data: %#v", response)
	}
}

func TestGetTelegramUpdatesWithoutTokenReturnsEmptyList(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime"}}))
	defer server.Close()

	response := callJSON(t, server.URL+"/api/getTgUpdates", login(t, server.URL), map[string]any{})
	items, ok := response["data"].([]any)
	if response["code"] != float64(http.StatusOK) || !ok || len(items) != 0 {
		t.Fatalf("Telegram updates without token = %#v", response)
	}
}

func TestRequiredMediaParametersAreValidatedBeforeDecoding(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "media"}}))
	defer server.Close()
	token := login(t, server.URL)

	for _, endpoint := range []struct {
		path   string
		method string
	}{
		{path: "/api/getSubtitles", method: http.MethodPost},
		{path: "/api/file", method: http.MethodGet},
		{path: "/api/proxyImage", method: http.MethodGet},
	} {
		var payload map[string]any
		transportStatus := http.StatusOK
		if endpoint.method == http.MethodGet {
			response := get(t, server.URL+endpoint.path, token)
			transportStatus = response.StatusCode
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				response.Body.Close()
				t.Fatal(err)
			}
			response.Body.Close()
		} else {
			payload = callJSON(t, server.URL+endpoint.path, token, map[string]any{})
		}
		if transportStatus != http.StatusOK || payload["code"] != float64(http.StatusInternalServerError) {
			t.Fatalf("missing parameter %s: status=%d payload=%#v", endpoint.path, transportStatus, payload)
		}
		if _, present := payload["data"]; present {
			t.Fatalf("missing parameter %s unexpectedly contains data: %#v", endpoint.path, payload)
		}
	}
}

func TestRequiredQueryErrorsMatchSpringParameterContract(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources", "rss", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources", "rss", "media"}}))
	defer server.Close()
	token := login(t, server.URL)

	cases := []struct {
		path     string
		body     any
		name     string
		typeName string
	}{
		{path: "/api/deleteAni", body: []string{}, name: "deleteFiles", typeName: "Boolean"},
		{path: "/api/batchEnable", body: []string{}, name: "value", typeName: "Boolean"},
		{path: "/api/updateTotalEpisodeNumber", body: []string{}, name: "force", typeName: "Boolean"},
		{path: "/api/scrape", body: model.Ani{}, name: "force", typeName: "Boolean"},
		{path: "/api/batchScrape", body: []string{}, name: "force", typeName: "Boolean"},
		{path: "/api/stop", name: "status", typeName: "Integer"},
		{path: "/api/deleteTorrent", name: "id", typeName: "String"},
		{path: "/api/mikan", body: model.Config{}, name: "text", typeName: "String"},
		{path: "/api/mikanGroup", name: "url", typeName: "String"},
		{path: "/api/aniBTGroup", name: "bgmId", typeName: "String"},
		{path: "/api/animeGardenGroup", name: "bgmId", typeName: "String"},
		{path: "/api/searchBgm", name: "name", typeName: "String"},
		{path: "/api/getAniBySubjectId", name: "id", typeName: "String"},
		{path: "/api/bgm/oauth/callback", name: "code", typeName: "String"},
	}
	for _, item := range cases {
		response := callJSON(t, server.URL+item.path, token, item.body)
		want := fmt.Sprintf("Required request parameter '%s' for method parameter type %s is not present", item.name, item.typeName)
		if response["code"] != float64(http.StatusInternalServerError) || response["message"] != want {
			t.Fatalf("missing query %s = %#v, want message %q", item.path, response, want)
		}
	}
}

func TestEmptySubscriptionWorkRequestsReturnStableValidationErrors(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "rss"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "rss"}}))
	defer server.Close()
	token := login(t, server.URL)

	cases := []struct {
		name string
		path string
		body any
	}{
		{name: "download path", path: "/api/downloadPath", body: model.Ani{}},
		{name: "preview RSS", path: "/api/previewAni", body: model.Ani{}},
		{name: "total episode selection", path: "/api/updateTotalEpisodeNumber?force=true", body: []string{}},
	}
	for _, item := range cases {
		response := callJSON(t, server.URL+item.path, token, item.body)
		if response["code"] != float64(http.StatusInternalServerError) || response["message"] == "" {
			t.Errorf("empty %s request = %#v, want a stable validation error", item.name, response)
		}
		if _, present := response["data"]; present {
			t.Errorf("empty %s request unexpectedly contains data: %#v", item.name, response)
		}
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
	found := false
	var listed map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !found {
		listed = callJSON(t, server.URL+"/api/listAni", token2, nil)
		weeks := listed["data"].(map[string]any)["weekList"].([]any)
		for _, rawWeek := range weeks {
			for _, rawItem := range rawWeek.(map[string]any)["items"].([]any) {
				if rawItem.(map[string]any)["id"] == item.ID {
					found = rawItem.(map[string]any)["totalEpisodeNumber"] == float64(12)
				}
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
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

func TestHTTPMikanRejectsMalformedOrEmptySeasonBodyBeforeCallingSource(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><a href="/Home/Bangumi/42">Demo</a></li></ul></div>`)
	}))
	defer upstream.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"mikanHost": upstream.URL}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set Mikan config = %#v", response)
	}

	for _, testCase := range []struct {
		name string
		body io.Reader
	}{
		{name: "malformed JSON", body: strings.NewReader(`{"season":`)},
		{name: "empty body", body: http.NoBody},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/api/mikan?text=demo", testCase.body)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", token)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var payload map[string]any
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != float64(http.StatusInternalServerError) {
				t.Fatalf("malformed Mikan request = %#v", payload)
			}
		})
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("Mikan source was called for invalid request: %d", upstreamCalls.Load())
	}
}

func TestHTTPSourceConversionCanCreateVisibleSubscription(t *testing.T) {
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","season":2,"platform":"TV","images":{"large":"https://img.test/demo.jpg"}}`)
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
	if !ok || item["title"] != "Demo CN (2026)" || item["releaseDate"] != "2026-01-02" || item["season"] != float64(2) || item["offset"] != float64(0) || item["totalEpisodeNumber"] != float64(12) {
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

func TestHTTPSubjectConversionFallsBackToCleanBangumiTitleAndPersistsCover(t *testing.T) {
	var bgmURL string
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/subjects/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":42,"name":"Legacy JP","name_cn":"Demo / 1/2:?","eps":3,"date":"2026-03-04","platform":"TV","images":{"large":"`+bgmURL+`/cover.jpg"}}`)
		case "/cover.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, "cover-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer bgm.Close()
	bgmURL = bgm.URL
	tmdb := httptest.NewServer(http.NotFoundHandler())
	defer tmdb.Close()

	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"bgmApi": bgm.URL, "tmdbApi": tmdb.URL, "tmdbApiKey": "test", "tmdb": true, "titleYear": true}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set metadata config = %#v", response)
	}
	response := callJSON(t, server.URL+"/api/getAniBySubjectId?id=42", token, nil)
	item, ok := response["data"].(map[string]any)
	if response["code"] != float64(http.StatusOK) || !ok {
		t.Fatalf("subject conversion = %#v", response)
	}
	if item["title"] != "Demo ½：？ (2026)" {
		t.Fatalf("clean Bangumi fallback title = %#v", item["title"])
	}
	cover, _ := item["cover"].(string)
	if cover == "" || cover == "cover.png" || !strings.Contains(cover, "/") {
		t.Fatalf("subject cover = %#v", item["cover"])
	}
	if _, err := os.Stat(filepath.Join(dir, "files", filepath.FromSlash(cover))); err != nil {
		t.Fatalf("subject cover was not saved: %v", err)
	}
}

func TestHTTPRefreshCoverDoesNotOverwriteSubscription(t *testing.T) {
	var sourceURL string
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cover.jpg" && r.URL.Path != "/cover2.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	defer images.Close()
	sourceURL = images.URL

	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "media"}}))
	defer server.Close()
	token := login(t, server.URL)
	item := model.Ani{ID: "cover-owner", Title: "Cover Owner", URL: "https://example.test/rss", Season: 1, Image: sourceURL + "/cover.jpg", Cover: "old.png"}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add cover subscription = %#v", response)
	}
	updated := item
	updated.Image = sourceURL + "/cover2.jpg"
	if response := callJSON(t, server.URL+"/api/refreshCover", token, updated); response["code"] != float64(http.StatusOK) {
		t.Fatalf("refresh cover = %#v", response)
	}
	items, err := app.Store().LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Cover != "old.png" {
		t.Fatalf("refresh cover changed subscription = %#v", items)
	}
}

func TestHTTPMikanRSSConversionResolvesLinkedBangumiSubject(t *testing.T) {
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/Home/Bangumi/123":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<div class="bangumi-title">Mikan Demo</div><div class="bangumi-info">官方网站：<a href="https://demo.test">official</a></div><div class="bangumi-info">Bangumi番组计划链接：<a href="https://bgm.tv/subject/42">BGM</a></div><div class="leftbar-item"><a class="subgroup-name" data-anchor="#370">LoliHouse</a></div><section id="370"><a class="mikan-rss" href="/RSS/Bangumi?bangumiId=123&amp;subgroupid=370"></a></section>`)
		case "/v0/subjects/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","season":2,"platform":"TV","images":{"large":"https://img.test/demo.jpg"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverSource.Close()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"mikanHost": serverSource.URL, "bgmApi": serverSource.URL, "tmdb": false}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set source config = %#v", response)
	}
	// Java only fetches the Mikan detail page when both optional fields are
	// absent. The UI's normal single-add flow already sends the linked BGM URL
	// and subgroup, so conversion must preserve those values and avoid a second
	// detail-page request.
	converted := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url":      serverSource.URL + "/RSS/Bangumi?bangumiId=123&subgroupid=370",
		"type":     "mikan",
		"bgmUrl":   "https://bgm.tv/subject/42",
		"subgroup": "LoliHouse",
	})
	item, ok := converted["data"].(map[string]any)
	if converted["code"] != float64(http.StatusOK) || !ok || item["bgmUrl"] != "https://bgm.tv/subject/42" || item["mikanTitle"] != "" || item["subgroup"] != "LoliHouse" || item["title"] != "Demo CN (2026)" || item["releaseDate"] != "2026-01-02" || item["season"] != float64(2) || item["offset"] != float64(0) || item["totalEpisodeNumber"] != float64(12) {
		t.Fatalf("Mikan RSS conversion = %#v", converted)
	}
}

func TestHTTPRSSConversionMatchesJavaSourceDefaults(t *testing.T) {
	var mikanDetailCalls atomic.Int32
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/Home/Bangumi/123":
			mikanDetailCalls.Add(1)
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<div class="bangumi-title">Mikan Demo</div><div class="bangumi-info">Bangumi番组计划链接：<a href="https://bgm.tv/subject/42">BGM</a></div><div class="leftbar-item"><a class="subgroup-name" data-anchor="#370">Detected</a></div><section id="370"><a class="mikan-rss" href="/RSS/Bangumi?bangumiId=123&amp;subgroupid=370"></a></section>`)
		case "/v0/subjects/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","platform":"TV","images":{"large":"https://img.test/demo.jpg"}}`)
		case "/v0/episodes":
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverSource.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"mikanHost": serverSource.URL, "bgmApi": serverSource.URL, "tmdb": false}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set source config = %#v", response)
	}

	provided := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": serverSource.URL + "/RSS/Bangumi?bangumiId=123&subgroupid=370", "type": "mikan",
		"bgmUrl": "https://bgm.tv/subject/42", "subgroup": "Provided",
	})
	if provided["code"] != float64(http.StatusOK) || provided["data"].(map[string]any)["subgroup"] != "Provided" {
		t.Fatalf("provided Mikan fields = %#v", provided)
	}
	if mikanDetailCalls.Load() != 0 {
		t.Fatalf("Mikan detail was requested with fields already provided: %d", mikanDetailCalls.Load())
	}
	emptyFields := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": serverSource.URL + "/RSS/Bangumi?bangumiId=123&subgroupid=370", "type": "mikan",
	})
	if emptyFields["code"] != float64(http.StatusOK) || emptyFields["data"].(map[string]any)["bgmUrl"] != "https://bgm.tv/subject/42" || emptyFields["data"].(map[string]any)["subgroup"] != "Detected" || mikanDetailCalls.Load() != 1 {
		t.Fatalf("Mikan detail fallback = %#v (detail calls=%d)", emptyFields, mikanDetailCalls.Load())
	}

	aniBT := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": serverSource.URL + "/rss/anime.xml?bgmId=42&groupSlug=AniBTGroup", "type": "ani-bt",
	})
	if aniBT["code"] != float64(http.StatusOK) || aniBT["data"].(map[string]any)["bgmUrl"] != "https://bgm.tv/subject/42" || aniBT["data"].(map[string]any)["subgroup"] != "AniBTGroup" {
		t.Fatalf("AniBT defaults = %#v", aniBT)
	}

	garden := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": serverSource.URL + "/feed.xml?subject=42&fansub=GardenGroup", "type": "anime-garden",
		"bgmUrl": "https://bgm.tv/subject/99", "subgroup": "IgnoredByJava",
	})
	if garden["code"] != float64(http.StatusOK) || garden["data"].(map[string]any)["bgmUrl"] != "https://bgm.tv/subject/42" || garden["data"].(map[string]any)["subgroup"] != "GardenGroup" {
		t.Fatalf("AnimeGarden defaults = %#v", garden)
	}

	other := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": serverSource.URL + "/feed.xml?bgmId=42", "type": "other",
	})
	if other["code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("other unexpectedly inferred BGM URL from RSS URL = %#v", other)
	}

	emptyURL := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": "", "type": "other", "bgmUrl": "https://bgm.tv/subject/42",
	})
	if emptyURL["code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("empty RSS URL = %#v", emptyURL)
	}
}

func TestHTTPSourceConversionReturnsCompleteEditableDefaults(t *testing.T) {
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","platform":"TV","images":{"large":"https://img.test/demo.jpg"}}`)
	}))
	defer serverSource.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"bgmApi": serverSource.URL, "tmdb": false}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set source config = %#v", response)
	}

	converted := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url":      serverSource.URL + "/feed.xml",
		"type":     "other",
		"bgmUrl":   "https://bgm.tv/subject/42",
		"subgroup": "Group",
	})
	item, ok := converted["data"].(map[string]any)
	if converted["code"] != float64(http.StatusOK) || !ok {
		t.Fatalf("converted item = %#v", converted)
	}
	for key := range map[string]bool{
		"standbyRssList": true, "match": true, "exclude": true, "notDownload": true,
		"tmdb": true, "customPriorityKeywords": true, "customTags": true,
	} {
		if _, exists := item[key]; !exists {
			t.Errorf("converted item missing %q: %#v", key, item)
		}
	}
	if item["season"] != float64(1) || item["offset"] != float64(0) || item["totalEpisodeNumber"] != float64(12) {
		t.Errorf("numeric defaults = %#v", item)
	}
	if item["omit"] != true || item["procrastinating"] != true || item["message"] != true || item["completed"] != true {
		t.Errorf("boolean defaults = %#v", item)
	}
	if item["customDownloadPath"] != false || item["customDownloadPathTemplate"] == "" || item["customCompletedPathTemplate"] == "" {
		t.Errorf("path defaults = %#v", item)
	}
	if item["customRenameTemplate"] == "" || item["customRenameTemplateEnable"] != false || item["customUploadEnable"] != false {
		t.Errorf("custom defaults = %#v", item)
	}
}

func TestHTTPRSSConversionAppliesJavaFeedDefaults(t *testing.T) {
	var sourceURL string
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/subjects/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","platform":"TV","images":{"large":"`+sourceURL+`/cover.png"}}`)
		case "/v0/episodes":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<rss><channel><item><title>[RSSGroup] Demo E03</title><guid>offset-03</guid><enclosure url="magnet:?xt=urn:btih:OFFSET03" length="10"/></item></channel></rss>`)
		case "/cover.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "png-fixture")
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverSource.Close()
	sourceURL = serverSource.URL

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{
		"bgmApi": serverSource.URL, "tmdb": false, "offset": true,
		"standbyRss": true, "copyMasterToStandby": true,
	}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set conversion defaults = %#v", response)
	}

	converted := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": serverSource.URL + "/feed.xml", "type": "other",
		"bgmUrl": "https://bgm.tv/subject/42",
	})
	item, ok := converted["data"].(map[string]any)
	if converted["code"] != float64(http.StatusOK) || !ok {
		t.Fatalf("conversion = %#v", converted)
	}
	if item["subgroup"] != "RSSGroup" || item["offset"] != float64(-2) || item["cover"] != "cover.png" && !strings.Contains(item["cover"].(string), "/") {
		t.Fatalf("feed defaults = %#v", item)
	}
	standby := item["standbyRssList"].([]any)
	if len(standby) != 1 {
		t.Fatalf("copied standby RSS = %#v", standby)
	}
	backup := standby[0].(map[string]any)
	if backup["url"] != serverSource.URL+"/feed.xml" || backup["label"] != "RSSGroup" || backup["offset"] != float64(-2) {
		t.Fatalf("standby defaults = %#v", backup)
	}
}

func TestHTTPSourceConversionResolvesTMDBBeforeApplyingTitleYear(t *testing.T) {
	var searchTitle string
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/subjects/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","platform":"TV"}`)
		case "/v0/episodes":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case "/3/search/tv":
			searchTitle = r.URL.Query().Get("query")
			_, _ = io.WriteString(w, `{"results":[{"id":99}]}`)
		case "/3/tv/99":
			_, _ = io.WriteString(w, `{"id":99,"name":"TMDB Name","original_name":"TMDB Original","first_air_date":"2025-03-04","number_of_episodes":10,"vote_average":8.8}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverSource.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{
		"bgmApi": serverSource.URL, "tmdbApi": serverSource.URL, "tmdbApiKey": "test-key", "tmdb": true, "titleYear": true,
	}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set metadata config = %#v", response)
	}
	converted := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": "https://example.test/feed.xml", "type": "other", "bgmUrl": "https://bgm.tv/subject/42", "subgroup": "Group",
	})
	item, ok := converted["data"].(map[string]any)
	if converted["code"] != float64(http.StatusOK) || !ok {
		t.Fatalf("TMDB conversion = %#v", converted)
	}
	if searchTitle != "Demo CN" {
		t.Fatalf("TMDB search title = %q", searchTitle)
	}
	if item["tmdb"].(map[string]any)["id"] != float64(99) || item["themoviedbName"] != "TMDB Name (2025)" || item["title"] != "TMDB Name (2025)" {
		t.Fatalf("TMDB/title-year conversion = %#v", item)
	}
}

func TestHTTPSourceConversionQueriesTMDBWhenTitleUseIsDisabled(t *testing.T) {
	var tmdbCalls atomic.Int32
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/subjects/42":
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","season":2,"platform":"TV"}`)
		case "/v0/episodes":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case "/3/search/tv":
			tmdbCalls.Add(1)
			_, _ = io.WriteString(w, `{"results":[{"id":99}]}`)
		case "/3/tv/99":
			tmdbCalls.Add(1)
			_, _ = io.WriteString(w, `{"id":99,"name":"TMDB Name","original_name":"TMDB Original","first_air_date":"2025-03-04","number_of_episodes":10,"vote_average":8.8}`)
		case "/3/tv/99/season/2":
			tmdbCalls.Add(1)
			_, _ = io.WriteString(w, `{"episodes":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverSource.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{
		"bgmApi": serverSource.URL, "tmdbApi": serverSource.URL, "tmdbApiKey": "test-key", "tmdb": false,
	}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set metadata config = %#v", response)
	}
	converted := callJSON(t, server.URL+"/api/rssToAni", token, map[string]any{
		"url": "https://example.test/feed.xml", "type": "other", "bgmUrl": "https://bgm.tv/subject/42", "subgroup": "Group",
	})
	item, ok := converted["data"].(map[string]any)
	if converted["code"] != float64(http.StatusOK) || !ok {
		t.Fatalf("TMDB-disabled conversion = %#v", converted)
	}
	if tmdbCalls.Load() == 0 {
		t.Fatalf("tmdb=false skipped the Java-compatible metadata lookup")
	}
	if item["tmdb"].(map[string]any)["id"] != float64(99) || item["themoviedbName"] != "TMDB Name (2025)" || item["title"] != "Demo CN (2025)" {
		t.Fatalf("tmdb=false title/data behavior = %#v", item)
	}
}

func TestHTTPUpdateTotalEpisodeNumberReturnsBeforeBackgroundWork(t *testing.T) {
	var started atomic.Int32
	serverSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/subjects/42":
			started.Add(1)
			time.Sleep(250 * time.Millisecond)
			_, _ = io.WriteString(w, `{"id":42,"name":"Demo","name_cn":"Demo","eps":12}`)
		case "/v0/episodes":
			_, _ = io.WriteString(w, `{"data":[{"id":1},{"id":2},{"id":3}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverSource.Close()

	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"bgmApi": serverSource.URL}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set BGM config = %#v", response)
	}
	item := model.Ani{ID: "async-total", Title: "Demo", URL: "https://example.test/feed.xml", BGMURL: "https://bgm.tv/subject/42", Season: 1, Enable: false}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add = %#v", response)
	}
	startedAt := time.Now()
	response := callJSON(t, server.URL+"/api/updateTotalEpisodeNumber?force=true", token, []string{item.ID})
	if response["code"] != float64(http.StatusOK) {
		t.Fatalf("async total response = %#v", response)
	}
	if elapsed := time.Since(startedAt); elapsed >= 200*time.Millisecond {
		t.Fatalf("updateTotalEpisodeNumber waited for background work: %s", elapsed)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		listed := callJSON(t, server.URL+"/api/listAni", token, nil)
		if weeks, ok := listed["data"].(map[string]any)["weekList"].([]any); ok {
			for _, rawWeek := range weeks {
				for _, rawItem := range rawWeek.(map[string]any)["items"].([]any) {
					if value := rawItem.(map[string]any); value["id"] == item.ID && value["totalEpisodeNumber"] == float64(3) {
						return
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background total episode update did not finish; subject requests=%d", started.Load())
}

func TestHTTPBGMEndpointsPreserveTokenAndDefaultScoreBehavior(t *testing.T) {
	var savedRate atomic.Int32
	var scoreBody map[string]any
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer bgm-token" {
			t.Errorf("BGM authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/me":
			_, _ = io.WriteString(w, `{"id":7,"username":"demo"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v0/users/demo/collections/42":
			_, _ = io.WriteString(w, `{"rate":7}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v0/users/demo/collections/404":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v0/users/-/collections/42":
			if err := json.NewDecoder(r.Body).Decode(&scoreBody); err != nil {
				t.Errorf("score body: %v", err)
			}
			savedRate.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer bgm.Close()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Config().Update(model.Config{"bgmApi": bgm.URL, "bgmToken": "bgm-token"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)

	rate := callJSON(t, server.URL+"/api/rate", token, map[string]any{"bgmUrl": "https://bgm.tv/subject/42"})
	if rate["code"] != float64(http.StatusOK) || rate["data"] != float64(7) || rate["message"] != "success" {
		t.Fatalf("rate = %#v", rate)
	}
	missing := callJSON(t, server.URL+"/api/rate", token, map[string]any{"bgmUrl": "https://bgm.tv/subject/404"})
	if missing["code"] != float64(http.StatusOK) || missing["data"] != float64(0) {
		t.Fatalf("missing rate = %#v", missing)
	}
	readOnMissingScore := callJSON(t, server.URL+"/api/setRate", token, map[string]any{"bgmUrl": "https://bgm.tv/subject/42"})
	if readOnMissingScore["code"] != float64(http.StatusOK) || readOnMissingScore["data"] != float64(7) || readOnMissingScore["message"] != "保存评分成功" {
		t.Fatalf("setRate without score = %#v", readOnMissingScore)
	}
	setZero := callJSON(t, server.URL+"/api/setRate", token, map[string]any{"bgmUrl": "https://bgm.tv/subject/42", "score": 0})
	if setZero["code"] != float64(http.StatusOK) || setZero["data"] != float64(0) || setZero["message"] != "保存评分成功" {
		t.Fatalf("setRate explicit zero = %#v", setZero)
	}
	if savedRate.Load() != 1 || scoreBody["type"] != float64(3) || scoreBody["rate"] != float64(0) {
		t.Fatalf("saved score request = count:%d body:%#v", savedRate.Load(), scoreBody)
	}
}

func TestHTTPBGMMeAndOAuthCallbackPreserveResponseAndPersistence(t *testing.T) {
	var oauthForm url.Values
	bgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token-status" {
			if err := r.ParseForm(); err != nil || r.Form.Get("access_token") != "bgm-token" {
				t.Errorf("token status form = %v %v", r.Form, err)
			}
			_, _ = io.WriteString(w, fmt.Sprintf(`{"expires":%d}`, time.Now().Add(5*24*time.Hour+time.Hour).Unix()))
			return
		}
		if r.URL.Path == "/v0/me" {
			_, _ = io.WriteString(w, `{"id":7,"username":"demo","nickname":"Demo","user_group":"10","reg_time":"2022-05-27T15:35:45+08:00","time_offset":8,"avatar":{"large":"https://img.test/avatar.jpg"}}`)
			return
		}
		if r.URL.Path == "/oauth-access-token" {
			if err := r.ParseForm(); err != nil {
				t.Errorf("OAuth form: %v", err)
			}
			oauthForm = r.Form
			_, _ = io.WriteString(w, `{"access_token":"new-token","refresh_token":"new-refresh"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer bgm.Close()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Config().Update(model.Config{
		"bgmApi":            bgm.URL,
		"bgmToken":          "bgm-token",
		"bgmTokenStatusApi": bgm.URL + "/token-status",
		"bgmOAuthApi":       bgm.URL + "/oauth-access-token",
		"bgmAppID":          "app-id",
		"bgmAppSecret":      "app-secret",
		"bgmRedirectUri":    "https://example.test/callback",
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "sources"}}))
	defer server.Close()
	token := login(t, server.URL)

	me := callJSON(t, server.URL+"/api/meBgm", token, nil)
	data, ok := me["data"].(map[string]any)
	if me["code"] != float64(http.StatusOK) || !ok || data["username"] != "demo" || data["userGroup"] != "10" || data["timeOffset"] != float64(8) || data["expiresDays"].(float64) < 5 {
		t.Fatalf("meBgm = %#v", me)
	}
	callback := callJSON(t, server.URL+"/api/bgm/oauth/callback?code=auth-code", token, nil)
	if callback["code"] != float64(http.StatusOK) || callback["message"] != "授权成功, 现在你可以关闭此窗口" {
		t.Fatalf("OAuth callback = %#v", callback)
	}
	if oauthForm.Get("grant_type") != "authorization_code" || oauthForm.Get("client_id") != "app-id" || oauthForm.Get("client_secret") != "app-secret" || oauthForm.Get("code") != "auth-code" || oauthForm.Get("redirect_uri") != "https://example.test/callback" {
		t.Fatalf("OAuth request = %#v", oauthForm)
	}
	cfg := app.Config().Snapshot()
	if cfg["bgmToken"] != "new-token" || cfg["bgmRefreshToken"] != "new-refresh" {
		t.Fatalf("OAuth tokens not persisted = %#v", cfg)
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

func TestBackendRepairsLegacySubscriptionFieldsOnStartup(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`[{"id":"legacy","title":"Legacy","url":"https://example.test/rss","year":2024,"month":2,"date":3,"image":"","season":0}]`)
	if err := os.WriteFile(filepath.Join(dir, "ani.v2.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	items, err := app.Store().LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("repaired subscriptions = %#v", items)
	}
	item := items[0]
	if item.ReleaseDate != "2024-02-03" || item.Season != 0 || item.Cover != "cover.png" || item.Exclude == nil || item.Match == nil || item.NotDownload == nil || item.TMDB == nil {
		t.Fatalf("legacy subscription was not repaired = %#v", item)
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
	deadline := time.Now().Add(2 * time.Second)
	for added.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if added.Load() != 1 {
		t.Fatalf("add count after refresh = %d", added.Load())
	}
	var listed map[string]any
	var progress map[string]any
	progressDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(progressDeadline) {
		listed = callJSON(t, server.URL+"/api/listAni", token, nil)
		if listed["code"] != float64(http.StatusOK) {
			t.Fatalf("list after refresh = %#v", listed)
		}
		weeks := listed["data"].(map[string]any)["weekList"].([]any)
		if len(weeks) > 0 && len(weeks[0].(map[string]any)["items"].([]any)) > 0 {
			progress = weeks[0].(map[string]any)["items"].([]any)[0].(map[string]any)
			if progress["currentEpisodeNumber"] == float64(1) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if progress == nil || progress["currentEpisodeNumber"] != float64(1) {
		t.Fatalf("current episode after refresh = %#v (response=%#v)", progress["currentEpisodeNumber"], listed)
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
	mediaTarget := filepath.Join(mediaRoot, "[Group] Demo S01E01.mkv")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mediaTarget); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(mediaTarget); err != nil {
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
	if missing["code"] != float64(http.StatusOK) || missing["message"] == "" {
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
	if err := os.WriteFile(filepath.Join(mediaDir, "Demo S01E01.jpn.ssa"), []byte("unsupported by browser"), 0o644); err != nil {
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
	// Java's getSubtitles endpoint returns only embedded MKV tracks. The
	// sidecar .ass file is returned by playList and must not be duplicated here.
	if subtitles["code"] != float64(http.StatusOK) || len(subtitles["data"].([]any)) != 0 {
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

func TestUploadKeepsJavaExtensionAndResultEnvelope(t *testing.T) {
	dir := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: dir, OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "media"}}))
	defer server.Close()
	token := login(t, server.URL)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "subtitle")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("subtitle"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	data, ok := payload["data"].(string)
	if payload["code"] != float64(http.StatusOK) || !ok || !strings.HasSuffix(data, ".") {
		t.Fatalf("extensionless upload response = %#v", payload)
	}
	if _, ok := payload["data"]; !ok {
		t.Fatalf("result envelope omitted data: %#v", payload)
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
