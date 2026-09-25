package backend_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestManagementRoutesBackupWebUIICSAndCache(t *testing.T) {
	root := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: root, Version: "1.2.3", OwnershipDomains: []string{"runtime", "subscriptions", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{ConfigDirectory: root, GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "media"}}))
	defer server.Close()
	token := login(t, server.URL)
	item := model.Ani{ID: "calendar", Title: "Calendar Demo", URL: "https://example.test/calendar", BGMURL: "https://bgm.tv/subject/42", ReleaseDate: "2026-09-14", Season: 1, Enable: true, Cover: "cover.png"}
	if response := callJSON(t, server.URL+"/api/addAni", token, item); response["code"] != float64(http.StatusOK) {
		t.Fatalf("add calendar item = %#v", response)
	}
	files := filepath.Join(root, "files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "cover.png"), []byte("used"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "unused.png"), []byte("unused"), 0o644); err != nil {
		t.Fatal(err)
	}
	if response := callJSON(t, server.URL+"/api/clearCache", token, nil); response["code"] != float64(http.StatusOK) {
		t.Fatalf("clear cache = %#v", response)
	}
	if _, err := os.Stat(filepath.Join(files, "cover.png")); err != nil {
		t.Fatalf("referenced cover was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(files, "unused.png")); !os.IsNotExist(err) {
		t.Fatalf("unused cache remains: %v", err)
	}

	logs := callJSON(t, server.URL+"/api/logs", token, nil)
	if logs["code"] != float64(http.StatusOK) {
		t.Fatalf("logs = %#v", logs)
	}
	if response := callJSON(t, server.URL+"/api/clearLogs", token, nil); response["code"] != float64(http.StatusOK) {
		t.Fatalf("clear logs = %#v", response)
	}
	customJS := get(t, server.URL+"/api/custom.js", "")
	customJSBody, _ := io.ReadAll(customJS.Body)
	customJS.Body.Close()
	if customJS.StatusCode != http.StatusOK || !strings.Contains(string(customJSBody), "empty js") {
		t.Fatalf("custom js status=%d body=%q", customJS.StatusCode, customJSBody)
	}

	backup := get(t, server.URL+"/api/exportConfig", token)
	backupData, err := io.ReadAll(backup.Body)
	if got := backup.Header.Get("Content-Disposition"); got != `inline; filename="ani-rss.backup.1.2.3.zip"` {
		backup.Body.Close()
		t.Fatalf("export content disposition = %q", got)
	}
	backup.Body.Close()
	if err != nil || backup.StatusCode != http.StatusOK {
		t.Fatalf("export status=%d err=%v", backup.StatusCode, err)
	}
	backupReader, err := zip.NewReader(bytes.NewReader(backupData), int64(len(backupData)))
	if err != nil {
		t.Fatal(err)
	}
	zipNames := map[string]bool{}
	for _, entry := range backupReader.File {
		zipNames[entry.Name] = true
	}
	if !zipNames["config.v2.json"] || !zipNames["ani.v2.json"] {
		t.Fatalf("backup entries = %v", zipNames)
	}

	importData := makeZip(t, map[string]string{"config.v2.json": `{"sortType":"PINYIN"}`, "ani.v2.json": `[{"id":"imported","title":"Imported","url":"https://example.test/imported","releaseDate":"2026-09-14","season":1,"enable":true}]`})
	importResponse := multipartRequest(t, server.URL+"/api/importConfig", token, "backup.zip", importData)
	var imported map[string]any
	if err := json.NewDecoder(importResponse.Body).Decode(&imported); err != nil {
		importResponse.Body.Close()
		t.Fatal(err)
	}
	importResponse.Body.Close()
	if imported["code"] != float64(http.StatusOK) {
		t.Fatalf("import = %#v", imported)
	}
	token = login(t, server.URL)
	listed := callJSON(t, server.URL+"/api/listAni", token, nil)
	if listed["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("imported subscriptions = %#v", listed)
	}

	webUI := makeZip(t, map[string]string{"webui.json": `{}`, "index.html": "custom-ui"})
	webUIResponse := multipartRequest(t, server.URL+"/api/webui/upload", token, "custom.zip", webUI)
	var webUIPayload map[string]any
	_ = json.NewDecoder(webUIResponse.Body).Decode(&webUIPayload)
	webUIResponse.Body.Close()
	if webUIPayload["code"] != float64(http.StatusOK) {
		t.Fatalf("webui upload = %#v", webUIPayload)
	}
	customPage := get(t, server.URL+"/", "")
	customPageBody, _ := io.ReadAll(customPage.Body)
	customPage.Body.Close()
	if customPage.StatusCode != http.StatusOK || string(customPageBody) != "custom-ui" {
		t.Fatalf("custom page status=%d body=%q", customPage.StatusCode, customPageBody)
	}
	if response := callJSON(t, server.URL+"/api/webui/delete", token, nil); response["code"] != float64(http.StatusOK) {
		t.Fatalf("webui delete = %#v", response)
	}

	config := callJSON(t, server.URL+"/api/config", token, nil)
	apiKey := config["data"].(map[string]any)["apiKey"].(string)
	calendar := get(t, server.URL+"/api/calendar.ics?api-key="+apiKey, "")
	calendarBody, _ := io.ReadAll(calendar.Body)
	calendar.Body.Close()
	if calendar.StatusCode != http.StatusOK || !strings.Contains(string(calendarBody), "BEGIN:VCALENDAR") || !strings.Contains(string(calendarBody), "RRULE:FREQ=WEEKLY") {
		t.Fatalf("calendar status=%d body=%q", calendar.StatusCode, calendarBody)
	}
	about := callJSON(t, server.URL+"/api/about", token, nil)
	if about["data"].(map[string]any)["version"] != "1.2.3" {
		t.Fatalf("about = %#v", about)
	}
	if response := callJSON(t, server.URL+"/api/stop?status=9", token, nil); response["code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("invalid stop = %#v", response)
	}
	image := get(t, server.URL+"/api/proxyImage?imgUrl=invalid", token)
	var imagePayload map[string]any
	_ = json.NewDecoder(image.Body).Decode(&imagePayload)
	image.Body.Close()
	if imagePayload["code"] != float64(http.StatusForbidden) {
		t.Fatalf("invalid proxy image = %#v", imagePayload)
	}
}

func TestManagementAndRuntimeResponsesMatchJavaDefaults(t *testing.T) {
	root := t.TempDir()
	app, err := backend.New(backend.Options{ConfigDir: root, Version: "1.2.3", OwnershipDomains: []string{"runtime", "subscriptions", "media"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{ConfigDirectory: root, GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "media"}}))
	defer server.Close()
	token := login(t, server.URL)

	config := callJSON(t, server.URL+"/api/config", token, nil)
	configData, ok := config["data"].(map[string]any)
	if !ok {
		t.Fatalf("config data = %#v", config)
	}
	if _, present := configData["runtimeOwnership"]; present {
		t.Fatalf("internal runtime ownership leaked through config API: %#v", configData["runtimeOwnership"])
	}

	customJS := get(t, server.URL+"/api/custom.js", "")
	customJS.Body.Close()
	if got := customJS.Header.Get("Content-Type"); got != "application/javascript;charset=utf-8" {
		t.Fatalf("custom JS content type = %q", got)
	}

	logs := callJSON(t, server.URL+"/api/logs", token, nil)
	if got, ok := logs["data"].([]any); !ok || len(got) == 0 {
		t.Fatalf("empty logs data = %#v", logs)
	}

	cache := callJSON(t, server.URL+"/api/clearCache", token, nil)
	if cache["message"] != "清理完成, 共清理 0.00 B" {
		t.Fatalf("empty cache response = %#v", cache)
	}

	trackers := callJSON(t, server.URL+"/api/trackersUpdate", token, map[string]any{})
	if trackers["message"] != "Trackers更新地址 为空" {
		t.Fatalf("empty trackers response = %#v", trackers)
	}

	for _, path := range []string{"/api/importConfig", "/api/webui/upload", "/api/upload", "/api/uploadAndRead", "/api/uploadAndReadToBase64"} {
		response := postRaw(t, server.URL+path, token, "", "")
		payload := decodeResponse(t, response)
		if payload["message"] != "Content-Type is not supported" {
			t.Errorf("%s empty multipart response = %#v", path, payload)
		}
	}

	webUIUpdate := callJSON(t, server.URL+"/api/webui/getUpdate", token, nil)
	if webUIUpdate["code"] != float64(http.StatusInternalServerError) || webUIUpdate["message"] != "无 WebUI 更新" {
		t.Fatalf("webui update response = %#v", webUIUpdate)
	}

	emby := callJSON(t, server.URL+"/api/getEmbyViews", token, map[string]any{})
	if emby["message"] != "embyHost 为空" {
		t.Fatalf("empty Emby response = %#v", emby)
	}

	calendar := get(t, server.URL+"/api/calendar.ics", token)
	calendar.Body.Close()
	if got := calendar.Header.Get("Content-Type"); got != "text/calendar;charset=UTF-8" {
		t.Fatalf("calendar content type = %q", got)
	}
}

func TestTrackerUpdateFetchesPlainTextAndUpdatesQBPreferences(t *testing.T) {
	var updated atomic.Int32
	var trackerBody string
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "udp://tracker.one:80/announce\nhttps://tracker.two/announce\n")
	}))
	defer tracker.Close()
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v4")
		case "/api/v2/app/preferences":
			_, _ = io.WriteString(w, `{"add_trackers":"old","add_trackers_enabled":false}`)
		case "/api/v2/app/setPreferences":
			_ = r.ParseForm()
			trackerBody = r.FormValue("json")
			updated.Add(1)
			_, _ = io.WriteString(w, "")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime"}}))
	defer server.Close()
	token := login(t, server.URL)
	if response := callJSON(t, server.URL+"/api/setConfig", token, model.Config{"downloadToolHost": qb.URL, "downloadToolPassword": "key"}); response["code"] != float64(http.StatusOK) {
		t.Fatalf("set tracker downloader = %#v", response)
	}
	response := callJSON(t, server.URL+"/api/trackersUpdate", token, model.Config{"trackersUpdateUrls": tracker.URL})
	if response["code"] != float64(http.StatusOK) || updated.Load() != 1 || !strings.Contains(trackerBody, "tracker.one") || !strings.Contains(trackerBody, "tracker.two") {
		t.Fatalf("tracker update=%#v calls=%d body=%s", response, updated.Load(), trackerBody)
	}
}

func postRaw(t *testing.T, target, token, contentType, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := backendTestHTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(writer, content)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func multipartRequest(t *testing.T, target, token, filename string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := backendTestHTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
