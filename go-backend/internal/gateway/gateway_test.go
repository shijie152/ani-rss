package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
)

func TestGatewayServesUIAndFallsBackToIndexForClientRoutes(t *testing.T) {
	uiDir := t.TempDir()
	writeFile(t, filepath.Join(uiDir, "index.html"), "<!doctype html><title>ANI-RSS</title>")
	writeFile(t, filepath.Join(uiDir, "assets", "app.js"), "console.log('app')")

	handler := gateway.New(gateway.Config{
		UIDirectory: uiDir,
		JavaURL:     "http://127.0.0.1:1",
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	for _, path := range []string{"/", "/settings"} {
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Accept", "text/html")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()

		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: got status %d", path, response.StatusCode)
		}
		if string(body) != "<!doctype html><title>ANI-RSS</title>" {
			t.Fatalf("GET %s: got body %q", path, body)
		}
	}

	response, err := http.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET asset: got status %d", response.StatusCode)
	}
}

func TestGatewayForwardsUnmigratedRequestsWithoutLosingRequestOrResponse(t *testing.T) {
	java := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.RawQuery != "name=demo" {
			t.Errorf("upstream request = %s %s", request.Method, request.URL.RequestURI())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := request.Header.Get("Cookie"); got != "session=abc" {
			t.Errorf("cookie = %q", got)
		}
		body, _ := io.ReadAll(request.Body)
		if string(body) != `{"title":"demo"}` {
			t.Errorf("body = %q", body)
		}

		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Set-Cookie", "session=upstream; Path=/")
		response.Header().Set("X-Upstream", "java")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"code":201,"message":"created"}`))
	}))
	t.Cleanup(java.Close)

	handler := gateway.New(gateway.Config{
		JavaURL: java.URL,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/addAni?name=demo", strings.NewReader(`{"title":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Cookie", "session=abc")
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("X-Upstream"); got != "java" {
		t.Fatalf("X-Upstream = %q", got)
	}
	if got := response.Header.Get("Set-Cookie"); got != "session=upstream; Path=/" {
		t.Fatalf("Set-Cookie = %q", got)
	}
	if string(body) != `{"code":201,"message":"created"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestGatewayForwardsMultipartAndBinaryResponses(t *testing.T) {
	java := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1024 * 1024); err != nil {
			t.Errorf("parse multipart form: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		file, _, err := request.FormFile("file")
		if err != nil {
			t.Errorf("read multipart file: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		if string(content) != "uploaded-content" {
			t.Errorf("uploaded content = %q", content)
		}

		response.Header().Set("Content-Type", "application/octet-stream")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(bytes.Repeat([]byte("stream"), 1024))
	}))
	t.Cleanup(java.Close)

	handler := gateway.New(gateway.Config{JavaURL: java.URL})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "config.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("uploaded-content")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/importConfig", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	streamed, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if response.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("content type = %q", response.Header.Get("Content-Type"))
	}
	if !bytes.Equal(streamed, bytes.Repeat([]byte("stream"), 1024)) {
		t.Fatalf("streamed body length = %d", len(streamed))
	}
}

func TestGatewayCanSwitchRouteOwnershipWithoutChangingTheUIRequest(t *testing.T) {
	java := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"owner":"java"}`))
	}))
	t.Cleanup(java.Close)

	goHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"owner":"go"}`))
	})
	handler := gateway.New(gateway.Config{
		JavaURL: java.URL,
		GoRoutes: []gateway.Route{
			{Domain: "health", Method: http.MethodPost, Path: "/api/ping", Handler: goHandler},
		},
		GoDomains: []string{"health"},
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	response := post(t, server.URL+"/api/ping")
	if body := readBody(t, response); body != `{"owner":"go"}` {
		t.Fatalf("go route body = %q", body)
	}

	handler.SetGoDomains(nil)
	response = post(t, server.URL+"/api/ping")
	if body := readBody(t, response); body != `{"owner":"java"}` {
		t.Fatalf("fallback route body = %q", body)
	}

	handler.SetGoDomains([]string{"health"})
	response = post(t, server.URL+"/api/ping")
	if body := readBody(t, response); body != `{"owner":"go"}` {
		t.Fatalf("restored go route body = %q", body)
	}
}

func TestGatewayReturnsUICompatibleJSONWhenJavaIsUnavailable(t *testing.T) {
	handler := gateway.New(gateway.Config{
		JavaURL: "http://127.0.0.1:1",
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	response := post(t, server.URL+"/api/listAni")
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content type = %q", got)
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Time    int64  `json:"t"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != http.StatusBadGateway || payload.Message == "" || payload.Time == 0 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestGatewayDoesNotServeFilesOutsideTheUIDirectory(t *testing.T) {
	uiDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, filepath.Join(uiDir, "index.html"), "index")
	writeFile(t, outside, "secret")

	handler := gateway.New(gateway.Config{UIDirectory: uiDir})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	request := httptest.NewRequest(http.MethodGet, "http://gateway/", nil)
	request.URL.Path = "/../" + filepath.Base(filepath.Dir(outside)) + "/secret.txt"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound || string(body) == "secret" {
		t.Fatalf("outside file response: status=%d body=%q", response.StatusCode, body)
	}
}

func post(t *testing.T, url string) *http.Response {
	t.Helper()
	response, err := http.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
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
