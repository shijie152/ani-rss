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
	token := login(t, server.URL)
	item := model.Ani{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 1, Enable: true}
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
