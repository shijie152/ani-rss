package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/testutil"

	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
)

var gatewayTestHTTPClient = testutil.LocalHTTPClient(10 * time.Second)

func TestGatewayServesUIAndFallsBackToIndexForClientRoutes(t *testing.T) {
	uiDir := t.TempDir()
	writeFile(t, filepath.Join(uiDir, "index.html"), "<!doctype html><title>ANI-RSS</title>")
	writeFile(t, filepath.Join(uiDir, "assets", "app.js"), "console.log('app')")
	server := httptest.NewServer(gateway.New(gateway.Config{UIDirectory: uiDir}))
	defer server.Close()
	for _, path := range []string{"/", "/settings"} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		request.Header.Set("Accept", "text/html")
		response, err := gatewayTestHTTPClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || string(body) != "<!doctype html><title>ANI-RSS</title>" {
			t.Fatalf("GET %s: status=%d body=%q", path, response.StatusCode, body)
		}
	}
	response, err := gatewayTestHTTPClient.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d", response.StatusCode)
	}
}

func TestGatewayServesRegisteredRoutesAndReturnsJSON404ForUnknownAPI(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: []gateway.Route{{Domain: "runtime", Method: http.MethodPost, Path: "/api/ping", Handler: handler}}, GoDomains: []string{"runtime"}}))
	defer server.Close()
	response, err := gatewayTestHTTPClient.Post(server.URL+"/api/ping", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("registered route status = %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = gatewayTestHTTPClient.Post(server.URL+"/api/not-found", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unknown route status = %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != float64(http.StatusNotFound) || payload["message"] != "404 Not Found !" {
		t.Fatalf("unknown route payload = %#v", payload)
	}
}

func TestGatewayCanDisableGoDomainWithoutSecondaryRouting(t *testing.T) {
	handler := gateway.New(gateway.Config{GoRoutes: []gateway.Route{{Domain: "runtime", Method: http.MethodPost, Path: "/api/ping", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "go") })}}, GoDomains: []string{"runtime"}})
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := gatewayTestHTTPClient.Post(server.URL+"/api/ping", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "go" {
		t.Fatalf("enabled body = %q", body)
	}
	handler.SetGoDomains(nil)
	response, err = gatewayTestHTTPClient.Post(server.URL+"/api/ping", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disabled route status = %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != float64(http.StatusNotFound) || payload["message"] != "404 Not Found !" {
		t.Fatalf("disabled route payload = %#v", payload)
	}
}

func TestGatewayDoesNotServeFilesOutsideTheUIDirectory(t *testing.T) {
	uiDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, filepath.Join(uiDir, "index.html"), "index")
	writeFile(t, outside, "secret")
	handler := gateway.New(gateway.Config{UIDirectory: uiDir})
	request := httptest.NewRequest(http.MethodGet, "http://gateway/", nil)
	request.URL.Path = "/../" + filepath.Base(filepath.Dir(outside)) + "/secret.txt"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || string(body) == "secret" {
		t.Fatalf("outside response: status=%d body=%q", response.StatusCode, body)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
