package backend_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/testutil"
)

var backendTestHTTPClient = testutil.LocalHTTPClient(10 * time.Second)

func init() {
	testutil.InstallTestNetworkGuard()
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
	response, err := backendTestHTTPClient.Do(request)
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
	response, err := backendTestHTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func getWithHeader(t *testing.T, target, token, key, value string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", token)
	request.Header.Set(key, value)
	response, err := backendTestHTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
