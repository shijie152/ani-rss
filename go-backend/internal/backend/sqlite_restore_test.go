package backend_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestSQLiteBackupImportRejectsInvalidStateBeforeReplacement(t *testing.T) {
	app, server := newSQLiteBackupServer(t)
	defer server.Close()
	defer app.Close()
	token := login(t, server.URL)
	item := model.Ani{ID: "original", Title: "Original", URL: "https://example.test/original", Enable: true}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add original = %#v", response)
	}
	invalid := makeZip(t, map[string]string{
		"config.v2.json": `{"proxy":true,"proxyHost":"","proxyPort":8080}`,
		"ani.v2.json":    `[{"id":"broken","title":"Broken","url":"https://example.test/broken","enable":true}]`,
	})
	response := multipartRequest(t, server.URL+"/api/importConfig", token, "invalid.zip", invalid)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("HTTP import status = %d", response.StatusCode)
	}
	payload := decodeBody(t, response)
	if payload["code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("invalid import = %#v", payload)
	}
	listed := callJSON(t, server.URL+"/api/listAni", token, nil)
	if listed["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("invalid import changed subscriptions = %#v", listed)
	}
}

func newSQLiteBackupServer(t *testing.T) (*backend.App, *httptest.Server) {
	t.Helper()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime", "subscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions"}}))
	return app, server
}
