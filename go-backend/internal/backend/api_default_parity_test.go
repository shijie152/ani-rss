//go:build live

package backend_test

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// FuzzJavaGoAPIDefaults compares the boundary behavior of both runtimes for
// requests that are deliberately empty, malformed, or missing required
// parameters. It is opt-in because it needs two already-running services.
// Every case below either stops at request validation or reads state; no
// downloader, updater, subscription write, cache deletion, or remote source
// request is reachable from this corpus.
func FuzzJavaGoAPIDefaults(f *testing.F) {
	if os.Getenv("ANI_RSS_ALLOW_NETWORK_TESTS") != "1" {
		f.Skip("live tests disabled; set ANI_RSS_ALLOW_NETWORK_TESTS=1")
	}
	javaURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ANI_RSS_JAVA_URL")), "/")
	goURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ANI_RSS_GO_URL")), "/")
	if javaURL == "" || goURL == "" {
		f.Skip("set ANI_RSS_JAVA_URL and ANI_RSS_GO_URL to run Java/Go default fuzz probes")
	}

	for _, seed := range []struct {
		selector uint8
		body     []byte
		query    uint8
	}{
		{0, nil, 0},
		{1, []byte("{}"), 1},
		{2, []byte("null"), 2},
		{3, []byte("[]"), 0},
		{4, []byte("{"), 1},
		{5, []byte("not-json"), 2},
	} {
		f.Add(seed.selector, seed.body, seed.query)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	password := os.Getenv("ANI_RSS_PARITY_PASSWORD_MD5")
	if password == "" {
		password = "21232f297a57a5a743894a0e4a801fc3"
	}
	username := os.Getenv("ANI_RSS_PARITY_USERNAME")
	if username == "" {
		username = "admin"
	}
	javaToken := parityLogin(f, client, javaURL, username, password)
	goToken := parityLogin(f, client, goURL, username, password)
	cases := defaultFuzzProbes()

	f.Fuzz(func(t *testing.T, selector uint8, body []byte, query uint8) {
		probe := cases[int(selector)%len(cases)]
		probe.path = fuzzUnusedQuery(probe.path, query)
		probe.body = fuzzBoundaryBody(selector, body, probe.body)
		java := runParityProbe(t, client, javaURL, javaToken, probe)
		goResponse := runParityProbe(t, client, goURL, goToken, probe)
		javaValue := parityFuzzShape(java, probe.ignoreDataSize)
		goValue := parityFuzzShape(goResponse, probe.ignoreDataSize)
		if !reflect.DeepEqual(javaValue, goValue) {
			t.Errorf("%s: Java projection = %#v; Go projection = %#v", probe.name, javaValue, goValue)
		}
	})
}

// defaultFuzzProbes intentionally excludes operations whose successful path
// mutates state or contacts a third-party service. Those paths have focused
// fake-server/temporary-directory tests and are listed in api-parity.md.
func defaultFuzzProbes() []apiParityProbe {
	return []apiParityProbe{
		{name: "ping default", method: http.MethodPost, path: "/api/ping", projection: parityResultShape},
		{name: "config default", method: http.MethodPost, path: "/api/config", token: true, ignoreDataSize: true, projection: parityResultShape},
		{name: "list default", method: http.MethodPost, path: "/api/listAni", token: true, projection: parityResultShape},
		{name: "new notification default", method: http.MethodPost, path: "/api/newNotification", token: true, ignoreDataSize: true, projection: parityResultShape},
		{name: "logs default", method: http.MethodPost, path: "/api/logs", token: true, ignoreDataSize: true, projection: parityResultShape},
		{name: "torrent list default", method: http.MethodPost, path: "/api/torrentsInfos", token: true, projection: parityResultShape},
		{name: "download logs default", method: http.MethodGet, path: "/api/downloadLogs", token: true, projection: parityFileHeaders},
		{name: "export default", method: http.MethodGet, path: "/api/exportConfig", token: true, projection: parityFileHeaders},
		{name: "calendar default", method: http.MethodGet, path: "/api/calendar.ics", token: true, projection: parityCalendar},
		{name: "import without multipart", method: http.MethodPost, path: "/api/importConfig", token: true, projection: parityEnvelope},
		{name: "webui upload without multipart", method: http.MethodPost, path: "/api/webui/upload", token: true, projection: parityEnvelope},
		{name: "webui update check", method: http.MethodPost, path: "/api/webui/getUpdate", token: true, projection: parityEnvelope},
		{name: "missing delete files", method: http.MethodPost, path: "/api/deleteAni", token: true, projection: parityResultShape},
		{name: "missing batch enable", method: http.MethodPost, path: "/api/batchEnable", token: true, projection: parityResultShape},
		{name: "missing total force", method: http.MethodPost, path: "/api/updateTotalEpisodeNumber", token: true, projection: parityResultShape},
		{name: "missing scrape force", method: http.MethodPost, path: "/api/scrape", token: true, projection: parityResultShape},
		{name: "missing batch scrape force", method: http.MethodPost, path: "/api/batchScrape", token: true, projection: parityResultShape},
		{name: "missing stop status", method: http.MethodPost, path: "/api/stop", token: true, projection: parityResultShape},
		{name: "missing torrent id", method: http.MethodPost, path: "/api/deleteTorrent", token: true, projection: parityResultShape},
		{name: "missing mikan text", method: http.MethodPost, path: "/api/mikan", token: true, projection: parityResultShape},
		{name: "malformed AniBT query", method: http.MethodPost, path: "/api/aniBT", body: "{", token: true, projection: parityResultShape},
		{name: "missing mikan group URL", method: http.MethodPost, path: "/api/mikanGroup", token: true, projection: parityResultShape},
		{name: "missing AniBT group id", method: http.MethodPost, path: "/api/aniBTGroup", token: true, projection: parityResultShape},
		{name: "missing AnimeGarden group id", method: http.MethodPost, path: "/api/animeGardenGroup", token: true, projection: parityResultShape},
		{name: "missing Bangumi name", method: http.MethodPost, path: "/api/searchBgm", token: true, projection: parityResultShape},
		{name: "missing Bangumi subject id", method: http.MethodPost, path: "/api/getAniBySubjectId", token: true, projection: parityResultShape},
		{name: "missing OAuth code", method: http.MethodPost, path: "/api/bgm/oauth/callback", token: true, projection: parityResultShape},
		{name: "missing subtitle filename", method: http.MethodPost, path: "/api/getSubtitles", token: true, projection: parityResultShape},
		{name: "missing media filename", method: http.MethodGet, path: "/api/file", token: true, projection: parityResultShape},
		{name: "missing image URL", method: http.MethodGet, path: "/api/proxyImage", token: true, projection: parityResultShape},
		{name: "empty TMDB name", method: http.MethodPost, path: "/api/getThemoviedbName", token: true, projection: parityResultShape},
		{name: "empty TMDB group", method: http.MethodPost, path: "/api/getThemoviedbGroup", token: true, projection: parityResultShape},
		{name: "empty RSS conversion", method: http.MethodPost, path: "/api/rssToAni", token: true, projection: parityResultShape},
		{name: "empty refresh", method: http.MethodPost, path: "/api/refreshAni", token: true, projection: parityResultShape},
		{name: "empty Emby views", method: http.MethodPost, path: "/api/getEmbyViews", token: true, projection: parityResultShape},
		{name: "empty Emby webhook", method: http.MethodPost, path: "/api/embyWebHook", token: true, projection: parityResultShape},
		{name: "empty BGM title", method: http.MethodPost, path: "/api/getBgmTitle", token: true, projection: parityResultShape},
		{name: "empty BGM rate", method: http.MethodPost, path: "/api/rate", token: true, projection: parityResultShape},
		{name: "empty BGM set rate", method: http.MethodPost, path: "/api/setRate", token: true, projection: parityResultShape},
		{name: "empty collection preview", method: http.MethodPost, path: "/api/previewCollection", token: true, projection: parityResultShape},
		{name: "empty collection subgroup", method: http.MethodPost, path: "/api/getCollectionSubgroup", token: true, projection: parityResultShape},
		{name: "empty playlist", method: http.MethodPost, path: "/api/playList", token: true, projection: parityResultShape},
		{name: "empty download login", method: http.MethodPost, path: "/api/downloadLoginTest", token: true, projection: parityResultShape},
		{name: "missing proxy URL", method: http.MethodPost, path: "/api/testProxy", token: true, projection: parityResultShape},
		{name: "missing trackers URL", method: http.MethodPost, path: "/api/trackersUpdate", token: true, projection: parityResultShape},
		{name: "missing multipart upload", method: http.MethodPost, path: "/api/upload", token: true, projection: parityResultShape},
		{name: "missing text upload", method: http.MethodPost, path: "/api/uploadAndRead", token: true, projection: parityResultShape},
		{name: "missing base64 upload", method: http.MethodPost, path: "/api/uploadAndReadToBase64", token: true, projection: parityResultShape},
	}
}

func fuzzBoundaryBody(selector uint8, input []byte, fixed string) string {
	boundaries := []string{"", "{}", "null", "[]", "{", "not-json"}
	value := uint64(selector)
	for index, item := range input {
		if index == 256 {
			break
		}
		value = value*33 + uint64(item)
	}
	// A fixed malformed body is needed for handlers such as /api/aniBT: a
	// valid object would intentionally enter their remote-source code path.
	if fixed == "<malformed>" {
		return "{"
	}
	if fixed != "" {
		return fixed
	}
	return boundaries[value%uint64(len(boundaries))]
}

func fuzzUnusedQuery(path string, selector uint8) string {
	if strings.Contains(path, "?") {
		return path + "&unused=" + fmt.Sprint(selector%3)
	}
	return path + "?unused=" + fmt.Sprint(selector%3)
}

func parityFuzzShape(response parityResponse, ignoreDataSize bool) any {
	value := map[string]any{
		"status":      response.status,
		"contentType": strings.ToLower(strings.ReplaceAll(response.header.Get("Content-Type"), " ", "")),
	}
	if response.payload == nil {
		value["bodyType"] = "non-json"
		return value
	}
	value["code"] = numberValue(response.payload["code"])
	data, exists := response.payload["data"]
	value["dataPresent"] = exists && data != nil
	if !value["dataPresent"].(bool) {
		value["dataType"] = "null"
		return value
	}
	value["dataType"] = fmt.Sprintf("%T", data)
	if !ignoreDataSize {
		switch typed := data.(type) {
		case []any:
			value["dataLen"] = len(typed)
		case map[string]any:
			value["dataLen"] = len(typed)
		}
	}
	return value
}
