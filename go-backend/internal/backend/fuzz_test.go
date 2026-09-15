package backend

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func FuzzJavaRange(f *testing.F) {
	f.Add("bytes=2-5", int64(10))
	f.Add("bytes=-5", int64(10))
	f.Add("bytes=2-", int64(10))
	f.Add("bytes=not-a-range", int64(10))
	f.Fuzz(func(t *testing.T, value string, size int64) {
		_, _, _, _ = javaRange(value, size)
	})
}

func FuzzGetSubtitlesRequestBoundary(f *testing.F) {
	f.Add("", []byte(nil))
	f.Add("not-base64", []byte("{}"))
	f.Add("L25vLXN1Y2gtbWVkaWEubWt2", []byte(`{"ignored":true}`))
	app, err := New(Options{ConfigDir: f.TempDir()})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(app.Close)
	f.Fuzz(func(t *testing.T, encodedFilename string, body []byte) {
		request := httptest.NewRequest(http.MethodPost, "/api/getSubtitles?filename="+url.QueryEscape(encodedFilename), bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		app.getSubtitles(recorder, request)
		response := recorder.Result()
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("transport status = %d", response.StatusCode)
		}
		var payload map[string]any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if _, ok := payload["code"]; !ok {
			t.Fatalf("response omitted code: %#v", payload)
		}
	})
}

func FuzzMediaFileRequestBoundary(f *testing.F) {
	f.Add("", "")
	f.Add("not-base64", "bytes=0-1")
	f.Add("L25vdC1hLXJlYWwtZmlsZS5ta3Y", "bytes=-5")
	app, err := New(Options{ConfigDir: f.TempDir()})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(app.Close)
	f.Fuzz(func(t *testing.T, encodedFilename, rangeHeader string) {
		request := httptest.NewRequest(http.MethodGet, "/api/file?filename="+url.QueryEscape(encodedFilename), nil)
		request.Header.Set("Range", rangeHeader)
		recorder := httptest.NewRecorder()
		app.file(recorder, request)
		assertJSONResult(t, recorder)
	})
}

func FuzzRequiredQueryBoundary(f *testing.F) {
	f.Add("", "")
	f.Add(" ", "?value=")
	f.Add("invalid", "?status=invalid")
	app, err := New(Options{ConfigDir: f.TempDir()})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(app.Close)
	f.Fuzz(func(t *testing.T, value, querySuffix string) {
		// The body is deliberately valid, while the required query is omitted.
		// This keeps the fuzz loop local and side-effect free and exercises the
		// same validation seam used by the HTTP routes.
		for _, item := range []struct {
			path string
			body string
			hand func(http.ResponseWriter, *http.Request)
		}{
			{path: "/api/deleteAni", body: "[]", hand: app.deleteAni},
			{path: "/api/batchEnable", body: "[]", hand: app.batchEnable},
			{path: "/api/updateTotalEpisodeNumber", body: "[]", hand: app.updateTotalEpisodeNumber},
			{path: "/api/scrape", body: "{}", hand: app.scrape},
			{path: "/api/batchScrape", body: "[]", hand: app.batchScrape},
			{path: "/api/stop", body: "", hand: app.stop},
			{path: "/api/deleteTorrent", body: "", hand: app.deleteTorrent},
			{path: "/api/mikan", body: "{}", hand: app.mikan},
			{path: "/api/mikanGroup", body: "", hand: app.mikanGroup},
			{path: "/api/aniBTGroup", body: "", hand: app.aniBTGroup},
			{path: "/api/animeGardenGroup", body: "", hand: app.animeGardenGroup},
			{path: "/api/searchBgm", body: "", hand: app.searchBgm},
			{path: "/api/getAniBySubjectId", body: "", hand: app.getAniBySubjectID},
			{path: "/api/bgm/oauth/callback", body: "", hand: app.bgmOAuthCallback},
		} {
			query := "?unused=" + url.QueryEscape(value+"\x00"+querySuffix)
			request := httptest.NewRequest(http.MethodPost, item.path+query, strings.NewReader(item.body))
			recorder := httptest.NewRecorder()
			item.hand(recorder, request)
			assertJSONResult(t, recorder)
		}
	})
}

func FuzzMalformedJSONHandlers(f *testing.F) {
	f.Add([]byte(nil))
	f.Add([]byte("{"))
	f.Add([]byte(`{"unexpected":[}`))
	f.Add([]byte("not-json"))
	app, err := New(Options{ConfigDir: f.TempDir()})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(app.Close)
	f.Fuzz(func(t *testing.T, body []byte) {
		if json.Valid(body) {
			t.Skip("seed is valid JSON; this fuzz target covers malformed JSON")
		}
		for _, handler := range []func(http.ResponseWriter, *http.Request){app.login, app.configSet, app.addAni, app.rssToAni} {
			request := httptest.NewRequest(http.MethodPost, "/api/test", bytes.NewReader(body))
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			assertJSONResult(t, recorder)
		}
	})
}

func assertJSONResult(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	response := recorder.Result()
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if _, ok := payload["code"]; !ok {
		t.Fatalf("response omitted code: %#v", payload)
	}
}
