//go:build live

package backend_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestJavaGoAPIParity is opt-in because it needs two already-running local
// services. The default probe set contains deterministic reads and validation
// failures. Probes that mutate application state are available explicitly via
// ANI_RSS_PARITY_INCLUDE_MUTATIONS=1 and must run against disposable services.
func TestJavaGoAPIParity(t *testing.T) {
	if os.Getenv("ANI_RSS_ALLOW_NETWORK_TESTS") != "1" {
		t.Skip("live tests disabled; set ANI_RSS_ALLOW_NETWORK_TESTS=1")
	}
	javaURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ANI_RSS_JAVA_URL")), "/")
	goURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ANI_RSS_GO_URL")), "/")
	if javaURL == "" || goURL == "" {
		t.Skip("set ANI_RSS_JAVA_URL and ANI_RSS_GO_URL to run Java/Go API parity probes")
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
	javaToken := parityLogin(t, client, javaURL, username, password)
	goToken := parityLogin(t, client, goURL, username, password)

	probes := []apiParityProbe{
		{name: "ping", method: http.MethodGet, path: "/api/ping", projection: parityEnvelope},
		{name: "ping post", method: http.MethodPost, path: "/api/ping", projection: parityEnvelope},
		{name: "ping options", method: http.MethodOptions, path: "/api/ping", projection: parityEmptyBody},
		{name: "custom js", method: http.MethodGet, path: "/api/custom.js", projection: parityStaticBody},
		{name: "custom css", method: http.MethodGet, path: "/api/custom.css", projection: parityStaticBody},
		{name: "IP whitelist default", method: http.MethodPost, path: "/api/testIpWhitelist", projection: parityEnvelope},
		{name: "config stable fields", method: http.MethodPost, path: "/api/config", token: true, projection: parityConfig},
		{name: "empty subscription list", method: http.MethodPost, path: "/api/listAni", token: true, projection: parityListAni},
		{name: "new notification", method: http.MethodPost, path: "/api/newNotification", token: true, projection: parityNotification},
		{name: "empty Telegram updates", method: http.MethodPost, path: "/api/getTgUpdates", body: "{}", token: true, projection: parityEmptyList},
		{name: "unconfigured torrent list", method: http.MethodPost, path: "/api/torrentsInfos", body: "{}", token: true, projection: parityEmptyList},
		{name: "unknown API", method: http.MethodGet, path: "/api/parity-unknown", projection: parityEnvelope},
		{name: "missing delete files", method: http.MethodPost, path: "/api/deleteAni", body: "[]", token: true, projection: parityEnvelope},
		{name: "missing enable value", method: http.MethodPost, path: "/api/batchEnable", body: "[]", token: true, projection: parityEnvelope},
		{name: "missing total force", method: http.MethodPost, path: "/api/updateTotalEpisodeNumber", body: "[]", token: true, projection: parityEnvelope},
		{name: "missing scrape force", method: http.MethodPost, path: "/api/scrape", body: "{}", token: true, projection: parityEnvelope},
		{name: "missing batch scrape force", method: http.MethodPost, path: "/api/batchScrape", body: "[]", token: true, projection: parityEnvelope},
		{name: "missing stop status", method: http.MethodPost, path: "/api/stop", token: true, projection: parityEnvelope},
		{name: "missing torrent id", method: http.MethodPost, path: "/api/deleteTorrent", token: true, projection: parityEnvelope},
		{name: "missing mikan text", method: http.MethodPost, path: "/api/mikan", body: "{}", token: true, projection: parityEnvelope},
		{name: "missing mikan group URL", method: http.MethodPost, path: "/api/mikanGroup", token: true, projection: parityEnvelope},
		{name: "malformed AniBT query", method: http.MethodPost, path: "/api/aniBT", body: "{", token: true, projection: parityResultShape},
		{name: "missing AniBT group id", method: http.MethodPost, path: "/api/aniBTGroup", token: true, projection: parityEnvelope},
		{name: "missing AnimeGarden group id", method: http.MethodPost, path: "/api/animeGardenGroup", token: true, projection: parityEnvelope},
		{name: "missing Bangumi name", method: http.MethodPost, path: "/api/searchBgm", token: true, projection: parityEnvelope},
		{name: "missing Bangumi subject id", method: http.MethodPost, path: "/api/getAniBySubjectId", token: true, projection: parityEnvelope},
		{name: "missing OAuth code", method: http.MethodPost, path: "/api/bgm/oauth/callback", token: true, projection: parityEnvelope},
		{name: "missing subtitle filename", method: http.MethodPost, path: "/api/getSubtitles", token: true, projection: parityEnvelope},
		{name: "missing media filename", method: http.MethodGet, path: "/api/file", token: true, projection: parityEnvelope},
		{name: "missing image URL", method: http.MethodGet, path: "/api/proxyImage", token: true, projection: parityEnvelope},
		{name: "empty TMDB group input", method: http.MethodPost, path: "/api/getThemoviedbGroup", body: "{}", token: true, projection: parityEnvelope},
		{name: "empty RSS conversion input", method: http.MethodPost, path: "/api/rssToAni", body: "{}", token: true, projection: parityEnvelope},
		{name: "refresh unknown subscription", method: http.MethodPost, path: "/api/refreshAni", body: "{}", token: true, projection: parityEnvelope},
		{name: "refresh all", method: http.MethodPost, path: "/api/refreshAll", token: true, mutating: true, projection: parityEnvelope},
		{name: "logs", method: http.MethodPost, path: "/api/logs", token: true, projection: parityResultShape},
		{name: "clear logs", method: http.MethodPost, path: "/api/clearLogs", token: true, mutating: true, projection: parityEnvelope},
		{name: "download logs", method: http.MethodGet, path: "/api/downloadLogs", token: true, projection: parityFileHeaders},
		{name: "clear cache", method: http.MethodPost, path: "/api/clearCache", token: true, mutating: true, projection: parityEnvelope},
		{name: "trackers update without URL", method: http.MethodPost, path: "/api/trackersUpdate", body: "{}", token: true, projection: parityEnvelope},
		{name: "export config", method: http.MethodGet, path: "/api/exportConfig", token: true, projection: parityFileHeaders},
		{name: "calendar", method: http.MethodGet, path: "/api/calendar.ics", token: true, projection: parityCalendar},
		{name: "about", method: http.MethodPost, path: "/api/about", token: true, projection: parityResultShape},
		{name: "import config without multipart", method: http.MethodPost, path: "/api/importConfig", token: true, projection: parityEnvelope},
		{name: "webui upload without multipart", method: http.MethodPost, path: "/api/webui/upload", token: true, projection: parityEnvelope},
		{name: "webui delete", method: http.MethodPost, path: "/api/webui/delete", token: true, mutating: true, projection: parityEnvelope},
		{name: "webui update check", method: http.MethodPost, path: "/api/webui/getUpdate", token: true, projection: parityEnvelope},
		{name: "webui update", method: http.MethodPost, path: "/api/webui/update", token: true, projection: parityEnvelope},
		{name: "notification test empty", method: http.MethodPost, path: "/api/testNotification", body: "{}", token: true, projection: parityResultShape},
		{name: "Emby views empty", method: http.MethodPost, path: "/api/getEmbyViews", body: "{}", token: true, projection: parityEnvelope},
		{name: "Emby webhook empty", method: http.MethodPost, path: "/api/embyWebHook", body: "{}", token: true, projection: parityEnvelope},
		{name: "add subscription empty", method: http.MethodPost, path: "/api/addAni", body: "{}", token: true, mutating: true, projection: parityResultShape},
		{name: "set subscription empty", method: http.MethodPost, path: "/api/setAni", body: "{}", token: true, projection: parityResultShape},
		{name: "empty batch enable", method: http.MethodPost, path: "/api/batchEnable?value=false", body: "[]", token: true, projection: parityEnvelope},
		{name: "empty total update", method: http.MethodPost, path: "/api/updateTotalEpisodeNumber?force=true", body: "[]", token: true, projection: parityEnvelope},
		{name: "empty import", method: http.MethodPost, path: "/api/importAni", body: `{"aniList":[]}`, token: true, projection: parityResultShape},
		{name: "download path empty", method: http.MethodPost, path: "/api/downloadPath", body: "{}", token: true, projection: parityResultShape},
		{name: "Bangumi title empty", method: http.MethodPost, path: "/api/getBgmTitle", body: "{}", token: true, projection: parityEnvelope},
		{name: "Bangumi rate empty", method: http.MethodPost, path: "/api/rate", body: "{}", token: true, projection: parityEnvelope},
		{name: "Bangumi set rate empty", method: http.MethodPost, path: "/api/setRate", body: "{}", token: true, projection: parityEnvelope},
		{name: "Bangumi account empty", method: http.MethodPost, path: "/api/meBgm", token: true, projection: parityEnvelope},
		{name: "start collection empty", method: http.MethodPost, path: "/api/startCollection", body: "{}", token: true, projection: parityResultShape},
		{name: "preview collection empty", method: http.MethodPost, path: "/api/previewCollection", body: "{}", token: true, projection: parityResultShape},
		{name: "collection subgroup empty", method: http.MethodPost, path: "/api/getCollectionSubgroup", body: "{}", token: true, projection: parityResultShape},
		{name: "refresh cover empty", method: http.MethodPost, path: "/api/refreshCover", body: "{}", token: true, projection: parityEnvelope},
		{name: "TMDB name empty", method: http.MethodPost, path: "/api/getThemoviedbName", body: "{}", token: true, projection: parityEnvelope},
		{name: "playlist empty", method: http.MethodPost, path: "/api/playList", body: "{}", token: true, projection: parityResultShape},
		{name: "missing torrent hash", method: http.MethodPost, path: "/api/deleteTorrent?id=x", token: true, projection: parityEnvelope},
		{name: "preview RSS empty", method: http.MethodPost, path: "/api/previewAni", body: "{}", token: true, projection: parityResultShape},
		{name: "download login empty", method: http.MethodPost, path: "/api/downloadLoginTest", body: "{}", token: true, projection: parityResultShape},
		{name: "upload without multipart", method: http.MethodPost, path: "/api/upload", token: true, projection: parityEnvelope},
		{name: "upload and read without multipart", method: http.MethodPost, path: "/api/uploadAndRead", token: true, projection: parityEnvelope},
		{name: "upload base64 without multipart", method: http.MethodPost, path: "/api/uploadAndReadToBase64", token: true, projection: parityEnvelope},
		// Java currently leaks an IndexOutOfBoundsException for an unsupported
		// status; Go intentionally returns a stable validation message. The
		// transport and business result shape remain the contract here.
		{name: "stop invalid status", method: http.MethodPost, path: "/api/stop?status=9", token: true, projection: parityResultShape},
		{name: "proxy test missing URL", method: http.MethodPost, path: "/api/testProxy", token: true, projection: parityEnvelope},
		// Keep setConfig last: Spring rotates the session token after every
		// configuration write, while all preceding probes reuse one login.
		{name: "set config empty", method: http.MethodPost, path: "/api/setConfig", body: "{}", token: true, mutating: true, projection: parityEnvelope},
	}

	for _, probe := range probes {
		probe := probe
		t.Run(probe.name, func(t *testing.T) {
			if probe.mutating && os.Getenv("ANI_RSS_PARITY_INCLUDE_MUTATIONS") != "1" {
				t.Skip("mutating probe disabled; set ANI_RSS_PARITY_INCLUDE_MUTATIONS=1 on disposable services")
			}
			java := runParityProbe(t, client, javaURL, javaToken, probe)
			goResponse := runParityProbe(t, client, goURL, goToken, probe)
			javaValue := probe.projection(java)
			goValue := probe.projection(goResponse)
			if !reflect.DeepEqual(javaValue, goValue) {
				t.Errorf("Java projection = %#v; Go projection = %#v", javaValue, goValue)
			}
		})
	}
}

type apiParityProbe struct {
	name           string
	method         string
	path           string
	body           string
	token          bool
	mutating       bool
	ignoreDataSize bool
	projection     func(parityResponse) any
}

type parityResponse struct {
	status  int
	header  http.Header
	body    []byte
	payload map[string]any
}

type parityFatalT interface {
	Helper()
	Fatal(...any)
	Fatalf(string, ...any)
}

func parityLogin(t parityFatalT, client *http.Client, base, username, password string) string {
	t.Helper()
	response := runParityProbe(t, client, base, "", apiParityProbe{
		name: "login", method: http.MethodPost, path: "/api/login",
		body:       mustJSON(map[string]string{"username": username, "password": password}),
		projection: parityEnvelope,
	})
	if response.status != http.StatusOK || numberValue(response.payload["code"]) != http.StatusOK {
		t.Fatalf("login failed with transport status %d and business code %v", response.status, response.payload["code"])
	}
	token, ok := response.payload["data"].(string)
	if !ok || token == "" {
		t.Fatal("login response did not contain a token")
	}
	return token
}

func runParityProbe(t parityFatalT, client *http.Client, base, token string, probe apiParityProbe) parityResponse {
	t.Helper()
	var body io.Reader
	if probe.body != "" {
		body = bytes.NewBufferString(probe.body)
	}
	request, err := http.NewRequest(probe.method, base+probe.path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if probe.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if probe.token {
		request.Header.Set("Authorization", token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request %s %s: %v", probe.method, probe.path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", probe.method, probe.path, err)
	}
	result := parityResponse{status: response.StatusCode, header: response.Header.Clone(), body: data}
	if len(bytes.TrimSpace(data)) > 0 && strings.HasPrefix(strings.TrimSpace(response.Header.Get("Content-Type")), "application/json") {
		if err := json.Unmarshal(data, &result.payload); err != nil {
			t.Fatalf("decode %s %s: %v", probe.method, probe.path, err)
		}
	}
	return result
}

func parityEnvelope(response parityResponse) any {
	return map[string]any{
		"status":  response.status,
		"code":    numberValue(response.payload["code"]),
		"message": response.payload["message"],
	}
}

func parityEmptyBody(response parityResponse) any {
	return map[string]any{"status": response.status, "body": string(response.body)}
}

func parityStaticBody(response parityResponse) any {
	contentType := strings.ReplaceAll(strings.ToLower(response.header.Get("Content-Type")), " ", "")
	return map[string]any{"status": response.status, "contentType": contentType, "body": string(response.body)}
}

func parityConfig(response parityResponse) any {
	data, _ := response.payload["data"].(map[string]any)
	projection := map[string]any{"status": response.status, "code": numberValue(response.payload["code"]), "message": response.payload["message"]}
	for _, key := range []string{"mikanHost", "tmdbApi", "tmdbImage", "rssSleepMinutes", "renameSleepSeconds", "proxy", "proxyPort", "downloadToolType", "sortType", "expirationTime", "outTradeNo", "tryOut", "verifyExpirationTime"} {
		projection[key] = data[key]
	}
	return projection
}

func parityEmptyList(response parityResponse) any {
	data, _ := response.payload["data"].([]any)
	return map[string]any{
		"status":   response.status,
		"code":     numberValue(response.payload["code"]),
		"message":  response.payload["message"],
		"dataType": fmt.Sprintf("%T", response.payload["data"]),
		"dataLen":  len(data),
	}
}

func parityResultShape(response parityResponse) any {
	dataType := "null"
	if response.payload != nil {
		dataType = fmt.Sprintf("%T", response.payload["data"])
	}
	return map[string]any{
		"status":   response.status,
		"code":     numberValue(response.payload["code"]),
		"dataType": dataType,
	}
}

func parityFileHeaders(response parityResponse) any {
	disposition := response.header.Get("Content-Disposition")
	// The Java build embeds its release version while a development Go build
	// uses "dev". Version is explicitly outside the parity contract, but the
	// filename itself is useful to compare, so normalize only that segment.
	disposition = regexp.MustCompile(`(ani-rss\.backup\.)[^";]+(\.zip)`).ReplaceAllString(disposition, `${1}<VERSION>${2}`)
	return map[string]any{
		"status":      response.status,
		"contentType": strings.ToLower(strings.ReplaceAll(response.header.Get("Content-Type"), " ", "")),
		"disposition": disposition,
	}
}

func parityCalendar(response parityResponse) any {
	return map[string]any{
		"status":      response.status,
		"contentType": strings.ToLower(strings.ReplaceAll(response.header.Get("Content-Type"), " ", "")),
	}
}

func parityListAni(response parityResponse) any {
	data, _ := response.payload["data"].(map[string]any)
	projection := map[string]any{"status": response.status, "code": numberValue(response.payload["code"]), "message": response.payload["message"], "total": data["total"]}
	if weeks, ok := data["weekList"].([]any); ok {
		labels := make([]string, 0, len(weeks))
		counts := make([]int, 0, len(weeks))
		for _, raw := range weeks {
			week, _ := raw.(map[string]any)
			labels = append(labels, fmt.Sprint(week["weekLabel"]))
			items, _ := week["items"].([]any)
			counts = append(counts, len(items))
		}
		projection["weekLabels"] = labels
		projection["weekCounts"] = counts
	}
	if dates, ok := data["releaseDateList"].([]any); ok {
		projection["releaseDateCount"] = len(dates)
	}
	return projection
}

func parityNotification(response parityResponse) any {
	data, _ := response.payload["data"].(map[string]any)
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return map[string]any{"status": response.status, "code": numberValue(response.payload["code"]), "message": response.payload["message"], "keys": keys}
}

func numberValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
