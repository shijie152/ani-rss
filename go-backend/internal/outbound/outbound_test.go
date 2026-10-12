package outbound_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/outbound"
)

func TestShouldProxyMatchesExactAndSubdomains(t *testing.T) {
	target, _ := url.Parse("https://api.example.test/path")
	if !outbound.ShouldProxy(target, "example.test\nother.test") {
		t.Fatal("subdomain was not matched")
	}
	target, _ = url.Parse("https://example.test/path")
	if !outbound.ShouldProxy(target, "example.test") {
		t.Fatal("exact host was not matched")
	}
	target, _ = url.Parse("https://example.test.evil/path")
	if outbound.ShouldProxy(target, "example.test") {
		t.Fatal("suffix without label was incorrectly matched")
	}
}

func TestClientUsesConfiguredProxyOnlyForListedHosts(t *testing.T) {
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := url.Parse("http://proxy-user:proxy-pass@" + proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request bypassed the configured proxy")
	}))
	defer target.Close()
	client := outbound.New(outbound.Options{Proxy: credentials, ProxyList: "127.0.0.1"})
	body, err := client.Read(context.Background(), outbound.Call{URL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" || proxied.Load() != 1 {
		t.Fatalf("body = %q, proxied = %d", body, proxied.Load())
	}

	unlisted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	defer unlisted.Close()
	client = outbound.New(outbound.Options{Proxy: credentials, ProxyList: "example.test"})
	body, err = client.Read(context.Background(), outbound.Call{URL: unlisted.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "direct" {
		t.Fatalf("unlisted host was proxied: %q", body)
	}
	if proxied.Load() != 1 {
		t.Fatalf("proxy saw %d requests, want 1", proxied.Load())
	}
}

func TestProxyCredentialsReachTheProxy(t *testing.T) {
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		encoded := strings.TrimPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
		credentials, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(credentials) != "proxy-user:proxy-pass" {
			t.Errorf("proxy auth = %q", r.Header.Get("Proxy-Authorization"))
		}
		if !strings.HasPrefix(r.URL.String(), "http://") {
			t.Errorf("proxy did not receive an absolute target URL: %s", r.URL)
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request bypassed the configured proxy")
	}))
	defer target.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyCredentials, err := url.Parse("http://proxy-user:proxy-pass@" + proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	client := outbound.New(outbound.Options{Proxy: proxyCredentials, ProxyList: "127.0.0.1"})
	body, err := client.Read(context.Background(), outbound.Call{URL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" || proxied.Load() != 1 {
		t.Fatalf("body = %q, proxied = %d", body, proxied.Load())
	}
}

func TestReadRetriesTransportFailureThenSucceeds(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			dropped, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = dropped.Close()
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	body, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL, Attempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" || attempts.Load() != 3 {
		t.Fatalf("body = %q, attempts = %d", body, attempts.Load())
	}
}

func TestTransportFailureIsNotRetriedPastTheAttemptBudget(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		dropped, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = dropped.Close()
	}))
	defer server.Close()

	if _, err := outbound.New(outbound.Options{Attempts: 2}).Read(context.Background(), outbound.Call{URL: server.URL}); err == nil {
		t.Fatal("expected the transport failure to surface")
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestStatusRetryPolicyDecidesWhatIsRetried(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		policy      func(int) bool
		wantRequest int64
	}{
		{name: "server error is retried", status: http.StatusInternalServerError, policy: outbound.RetryServerErrors, wantRequest: 3},
		{name: "client error is terminal", status: http.StatusNotFound, policy: outbound.RetryServerErrors, wantRequest: 1},
		{name: "any status is retried", status: http.StatusNotFound, policy: outbound.RetryAnyStatus, wantRequest: 3},
		{name: "no policy means no retry", status: http.StatusInternalServerError, policy: nil, wantRequest: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var attempts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()

			_, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{
				URL: server.URL, Attempts: 3, RetryStatus: testCase.policy,
			})
			if err == nil {
				t.Fatal("expected a non-2xx response to surface as an error")
			}
			if attempts.Load() != testCase.wantRequest {
				t.Fatalf("attempts = %d, want %d", attempts.Load(), testCase.wantRequest)
			}
		})
	}
}

func TestAttemptsAreCappedSoABadPolicyCannotSpin(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	_, _ = outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{
		URL: server.URL, Attempts: 99, RetryStatus: outbound.RetryAnyStatus,
	})
	if attempts.Load() != 5 {
		t.Fatalf("attempts = %d, want the cap of 5", attempts.Load())
	}
}

func TestDefaultPolicyMakesOneAttempt(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, _ = outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL, RetryStatus: outbound.RetryAnyStatus})
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1 by default", attempts.Load())
	}
}

func TestSuccessfulResponseIsNeverRetried(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	body, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{
		URL: server.URL, Attempts: 3, RetryStatus: outbound.RetryAnyStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" || attempts.Load() != 1 {
		t.Fatalf("body = %q, attempts = %d, want one request for a 200", body, attempts.Load())
	}
}

func TestOnlyTwoHundredsCountAsSuccess(t *testing.T) {
	cases := []struct {
		status  int
		success bool
	}{
		{status: http.StatusOK, success: true},
		{status: 299, success: true},
		{status: http.StatusMultipleChoices, success: false},
		{status: 399, success: false},
	}
	for _, testCase := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(testCase.status)
		}))
		body, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL})
		server.Close()
		if testCase.success && err != nil {
			t.Fatalf("status %d was treated as a failure: %v", testCase.status, err)
		}
		if !testCase.success {
			var statusErr *outbound.StatusError
			if !errors.As(err, &statusErr) || statusErr.Status != testCase.status || len(body) != 0 {
				t.Fatalf("status %d was treated as a success: err = %v", testCase.status, err)
			}
		}
	}
}

func TestServerErrorIsLoggedEvenWhenNotVerbose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logs := &logCapture{}
	client := outbound.New(outbound.Options{Logger: logs.logger()})
	if _, err := client.Read(context.Background(), outbound.Call{Service: "mikan", URL: server.URL}); err == nil {
		t.Fatal("expected a 500 to surface as an error")
	}
	if line := logs.drain(); !strings.Contains(line, "WARN") {
		t.Fatalf("a 500 was not logged: %q", line)
	}
}

func TestRetriesPauseBetweenAttempts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	started := time.Now()
	_, _ = outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{
		URL: server.URL, Attempts: 3, RetryStatus: outbound.RetryAnyStatus,
	})
	// Three attempts wait 50ms and 100ms, so anything fast means the backoff was
	// skipped and a failing upstream would be hammered.
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("three attempts took %s, want the backoff between them", elapsed)
	}
}

func TestStatusErrorKeepsCallerWording(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	t.Run("explicit prefix preserves the legacy message", func(t *testing.T) {
		_, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL, StatusPrefix: "RSS服务返回 "})
		if err == nil || err.Error() != "RSS服务返回 HTTP 404" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("service name labels the default message", func(t *testing.T) {
		_, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL, Service: "mikan"})
		if err == nil || err.Error() != "mikan 返回 HTTP 404" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("status error carries the status for callers that report it", func(t *testing.T) {
		var statusErr *outbound.StatusError
		_, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL})
		if !errors.As(err, &statusErr) {
			t.Fatalf("err = %v, want *StatusError", err)
		}
		if statusErr.Status != http.StatusNotFound {
			t.Fatalf("status = %d", statusErr.Status)
		}
	})
}

func TestBodyIsCappedAndTruncated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 1024))
	}))
	defer server.Close()

	body, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL, MaxBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 16 {
		t.Fatalf("len(body) = %d, want the 16 byte cap", len(body))
	}
}

func TestRetriesResendTheBody(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if string(payload) != "add=1" {
			t.Errorf("attempt %d sent %q", attempts.Load()+1, payload)
		}
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "queued")
	}))
	defer server.Close()

	body, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{
		Method: http.MethodPost, URL: server.URL, Body: []byte("add=1"),
		Attempts: 3, RetryStatus: outbound.RetryServerErrors,
		Header: http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "queued" || attempts.Load() != 3 {
		t.Fatalf("body = %q, attempts = %d", body, attempts.Load())
	}
}

func TestTimeoutBoundsASingleAttempt(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	started := time.Now()
	if _, err := outbound.New(outbound.Options{}).Read(context.Background(), outbound.Call{URL: server.URL, Timeout: 60 * time.Millisecond}); err == nil {
		t.Fatal("expected the attempt to time out")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("attempt took %s, want the per-attempt timeout to bound it", elapsed)
	}
}

func TestUserAgentDefaultsAndCallHeadersWin(t *testing.T) {
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
	}))
	defer server.Close()

	client := outbound.New(outbound.Options{})
	if _, err := client.Read(context.Background(), outbound.Call{URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	if agent := (<-seen).Get("User-Agent"); agent != "ani-rss-go" {
		t.Fatalf("user agent = %q", agent)
	}
	if _, err := client.Read(context.Background(), outbound.Call{
		URL: server.URL, Header: http.Header{"User-Agent": []string{"custom-agent"}, "Accept": []string{"application/json"}},
	}); err != nil {
		t.Fatal(err)
	}
	header := <-seen
	if agent := header.Get("User-Agent"); agent != "custom-agent" {
		t.Fatalf("user agent = %q, want the caller value", agent)
	}
	if accept := header.Get("Accept"); accept != "application/json" {
		t.Fatalf("accept = %q", accept)
	}
}

func TestStreamHandsBackTheWholeBody(t *testing.T) {
	payload := strings.Repeat("y", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	response, err := outbound.New(outbound.Options{}).Stream(context.Background(), outbound.Call{URL: server.URL, MaxBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != len(payload) {
		t.Fatalf("streamed %d bytes, want the full %d", len(body), len(payload))
	}
}

func TestStreamRetriesServerErrorsBeforeHandingOff(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "archive")
	}))
	defer server.Close()

	response, err := outbound.New(outbound.Options{}).Stream(context.Background(), outbound.Call{
		URL: server.URL, Attempts: 3, RetryStatus: outbound.RetryServerErrors,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "archive" || attempts.Load() != 2 {
		t.Fatalf("body = %q, attempts = %d", body, attempts.Load())
	}
}

func TestLogRecordKeepsTheTargetAndDropsTheQueryString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()

	logs := &logCapture{}
	client := outbound.New(outbound.Options{Logger: logs.logger(), Verbose: true})
	if _, err := client.Read(context.Background(), outbound.Call{
		Service: "tmdb", URL: server.URL + "/3/tv/1?api_key=super-secret&language=zh",
	}); err != nil {
		t.Fatal(err)
	}
	line := logs.single(t)
	if strings.Contains(line, "super-secret") || strings.Contains(line, "api_key") {
		t.Fatalf("log record leaked the query string: %s", line)
	}
	if !strings.Contains(line, "tmdb") || !strings.Contains(line, "/3/tv/1") {
		t.Fatalf("log record lost the target: %s", line)
	}
}

func TestOnlyFailuresAreLoggedUnlessVerbose(t *testing.T) {
	var status atomic.Int64
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()

	t.Run("a successful call stays quiet by default", func(t *testing.T) {
		logs := &logCapture{}
		client := outbound.New(outbound.Options{Logger: logs.logger()})
		if _, err := client.Read(context.Background(), outbound.Call{Service: "mikan", URL: server.URL}); err != nil {
			t.Fatal(err)
		}
		if line := logs.drain(); line != "" {
			t.Fatalf("expected no log record, got %s", line)
		}
	})
	t.Run("a failure is logged even when not verbose", func(t *testing.T) {
		status.Store(http.StatusBadGateway)
		defer status.Store(http.StatusOK)
		logs := &logCapture{}
		client := outbound.New(outbound.Options{Logger: logs.logger()})
		if _, err := client.Read(context.Background(), outbound.Call{Service: "mikan", URL: server.URL}); err == nil {
			t.Fatal("expected the bad gateway to surface")
		}
		lines := logs.drain()
		if !strings.Contains(lines, "WARN") || !strings.Contains(lines, "mikan") {
			t.Fatalf("failure was not logged: %s", lines)
		}
	})
	t.Run("verbose records a successful call at debug level", func(t *testing.T) {
		logs := &logCapture{}
		client := outbound.New(outbound.Options{Logger: logs.logger(), Verbose: true})
		if _, err := client.Read(context.Background(), outbound.Call{Service: "mikan", URL: server.URL}); err != nil {
			t.Fatal(err)
		}
		lines := logs.drain()
		if !strings.Contains(lines, "DEBUG") || !strings.Contains(lines, "mikan") {
			t.Fatalf("verbose success was not logged: %s", lines)
		}
	})
}

func TestFromConfigRejectsAnIncompleteProxyBeforeAnyCall(t *testing.T) {
	if _, err := outbound.FromConfig(map[string]any{"proxy": true, "proxyHost": "", "proxyPort": 0}, nil); err == nil || !strings.Contains(err.Error(), "代理参数不完整") {
		t.Fatalf("err = %v, want the incomplete proxy to be reported before a call", err)
	}
	client, err := outbound.FromConfig(map[string]any{"proxy": false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	directTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	defer directTarget.Close()
	body, err := client.Read(context.Background(), outbound.Call{URL: directTarget.URL})
	if err != nil || string(body) != "direct" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}

func TestWatchReportsAnIncompleteProxyOnTheCall(t *testing.T) {
	configuration := map[string]any{"proxy": true, "proxyHost": "", "proxyPort": 0}
	client := outbound.Watch(func() map[string]any { return configuration }, nil)
	brokenProxyTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer brokenProxyTarget.Close()
	_, err := client.Read(context.Background(), outbound.Call{URL: brokenProxyTarget.URL})
	if err == nil || !strings.Contains(err.Error(), "代理参数不完整") {
		t.Fatalf("err = %v, want the incomplete proxy to surface on the call", err)
	}
}

func TestWatchFollowsAProxyTurnedOnLater(t *testing.T) {
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyPort, err := strconv.Atoi(proxyURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	configuration := map[string]any{"proxy": false, "proxyHost": proxyURL.Hostname(), "proxyPort": proxyPort, "proxyList": "127.0.0.1"}
	client := outbound.Watch(func() map[string]any { return configuration }, nil)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	defer target.Close()
	body, err := client.Read(context.Background(), outbound.Call{URL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "direct" {
		t.Fatalf("body = %q, want the request to go direct while the proxy is off", body)
	}

	configuration["proxy"] = true
	body, err = client.Read(context.Background(), outbound.Call{URL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" || proxied.Load() != 1 {
		t.Fatalf("body = %q, proxied = %d, want the request to follow the new proxy", body, proxied.Load())
	}
}

type logCapture struct {
	mu      sync.Mutex
	records []string
}

func (c *logCapture) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(logWriter{c}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (c *logCapture) drain() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	joined := strings.Join(c.records, "\n")
	c.records = nil
	return joined
}

func (c *logCapture) single(t *testing.T) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(c.drain()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one log record, got %d: %v", len(lines), lines)
	}
	return lines[0]
}

type logWriter struct{ capture *logCapture }

func (w logWriter) Write(payload []byte) (int, error) {
	w.capture.mu.Lock()
	defer w.capture.mu.Unlock()
	w.capture.records = append(w.capture.records, strings.TrimRight(string(payload), "\n"))
	return len(payload), nil
}
