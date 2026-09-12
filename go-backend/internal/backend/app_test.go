package backend_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
