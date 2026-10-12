// Package outbound is the single entry point for the calls this service makes
// to third-party services.
//
// Transport policy (proxy selection, per-attempt timeout), request shape
// (method, headers, body, user agent), retry policy, response size caps and the
// outbound log record all live here. A package that needs a third-party service
// describes the request and receives an outcome; it never receives a bare
// *http.Client, because a caller holding one is free to grow the next private
// retry loop, user agent or size cap.
package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

const (
	// DefaultTimeout bounds one attempt end to end, including reading the body.
	DefaultTimeout = 20 * time.Second
	// DefaultMaxBytes caps how much of a response body gets buffered.
	DefaultMaxBytes int64 = 16 << 20
	// DefaultUserAgent identifies this service to third parties.
	DefaultUserAgent = "ani-rss-go"
	// maxAttempts caps retries so a bad policy cannot spin forever.
	maxAttempts = 5
	// retryInterval is the backoff base; the n-th retry waits n intervals.
	retryInterval = 50 * time.Millisecond
)

// Options configures the transport and the defaults shared by every call.
type Options struct {
	// Proxy, when set, is used for the hosts matched by ProxyList.
	Proxy *url.URL
	// ProxyList is a whitespace separated list of hosts and host suffixes. An
	// empty list proxies nothing, which is the behavior users already have.
	ProxyList string

	Timeout   time.Duration
	MaxBytes  int64
	Attempts  int
	UserAgent string

	// Transport overrides the default transport. Tests use it to point the client
	// at an httptest server; production leaves it nil so the loopback guard tests
	// install on http.DefaultTransport is inherited along with the defaults.
	Transport http.RoundTripper

	// Logger receives the outbound log record. A nil logger keeps the client quiet.
	Logger *slog.Logger
	// Verbose records successful calls as well as failures. It is wired to the
	// DEBUG switch in settings: a successful call is noise unless someone asked
	// for it, while a failure is always worth a line.
	Verbose bool
}

// Client performs outbound requests under one policy.
//
// A client built with Watch follows a live configuration, so a component that
// lives as long as the process still picks up a proxy the user turns on. Only
// the proxy requires a new transport, so the transport is rebuilt then and
// reused otherwise.
type Client struct {
	snapshot func() model.Config
	options  Options

	mu        sync.Mutex
	transport *http.Client
	proxyKey  string
}

// New builds a client with a fixed policy. Callers that already hold a
// configuration snapshot use FromConfig, which reads policy from it.
func New(options Options) *Client { return &Client{options: options} }

// policy is the effective setting for one call.
type policy struct {
	client    *http.Client
	userAgent string
	timeout   time.Duration
	maxBytes  int64
	attempts  int
	verbose   bool
}

// policy resolves the settings for one call. A client built through Watch takes
// a fresh snapshot here, so config changes reach long-lived components without
// a restart; an unusable proxy is reported on the call rather than swallowed.
func (c *Client) policy() (policy, error) {
	options := c.options
	if c.snapshot != nil {
		followed, err := options.following(c.snapshot())
		if err != nil {
			return policy{}, err
		}
		options = followed
	}
	client, err := c.transportFor(options)
	if err != nil {
		return policy{}, err
	}
	return policy{
		client:    client,
		userAgent: valueOr(options.UserAgent, DefaultUserAgent),
		timeout:   durationOr(options.Timeout, DefaultTimeout),
		maxBytes:  countOr(options.MaxBytes, DefaultMaxBytes),
		attempts:  clampAttempts(options.Attempts),
		verbose:   options.Verbose,
	}, nil
}

// transportFor reuses the transport while the proxy settings hold and rebuilds
// it when they change. The clone keeps the loopback guard ordinary tests install
// on http.DefaultTransport, and leaves the shared transport untouched.
func (c *Client) transportFor(options Options) (*http.Client, error) {
	key := proxyKey(options)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil && c.proxyKey == key {
		return c.transport, nil
	}
	transport := options.Transport
	if transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			base = &http.Transport{Proxy: http.ProxyFromEnvironment}
		}
		cloned := base.Clone()
		if options.Proxy != nil {
			proxy, rules := options.Proxy, options.ProxyList
			cloned.Proxy = func(request *http.Request) (*url.URL, error) {
				if ShouldProxy(request.URL, rules) {
					return proxy, nil
				}
				return nil, nil
			}
		}
		transport = cloned
	}
	c.transport, c.proxyKey = &http.Client{Transport: transport}, key
	return c.transport, nil
}

// ShouldProxy reports whether target matches one of the configured host rules.
// Rules are whitespace separated host names; a leading "*." is accepted.
func ShouldProxy(target *url.URL, rules string) bool {
	if target == nil {
		return false
	}
	host := strings.ToLower(target.Hostname())
	for _, rule := range strings.Fields(rules) {
		rule = strings.ToLower(strings.TrimSpace(rule))
		rule = strings.TrimPrefix(rule, "*.")
		if rule == host || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}

// Call is one outbound request together with the policy that applies to it.
// Zero values fall back to the client defaults.
type Call struct {
	// Service names the caller in log records, for example "mikan" or "tmdb".
	Service string
	Method  string
	URL     string
	Header  http.Header
	Body    []byte

	// Timeout bounds one attempt, including reading the body.
	Timeout time.Duration
	// MaxBytes caps the buffered body. A larger response is truncated, which is
	// what the callers already did.
	MaxBytes int64
	// Attempts counts total tries, not retries.
	Attempts int

	// RetryStatus decides whether a failed response with this status is retried.
	// It is consulted for non-2xx statuses only, and transport failures are always
	// retried while attempts remain.
	RetryStatus func(status int) bool

	// StatusPrefix replaces the default wording of a non-2xx error, so callers
	// that already show a message to users keep saying exactly what they said.
	// The status is appended as "HTTP <status>".
	StatusPrefix string
}

// RetryNever keeps a status from being retried. It is the default behavior.
func RetryNever(int) bool { return false }

// RetryServerErrors retries 5xx responses only.
func RetryServerErrors(status int) bool { return status >= 500 }

// RetryAnyStatus retries every failed response, which is what the resource
// sources and the feed readers always did.
func RetryAnyStatus(int) bool { return true }

// Response is a completed outbound response whose body has been buffered.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// OK reports whether the response carries a 2xx status.
func (r *Response) OK() bool { return succeeded(r.Status) }

// JSON decodes the buffered body into target.
func (r *Response) JSON(target any) error { return json.Unmarshal(r.Body, target) }

// Cookies parses the response's Set-Cookie headers, so a login can keep its
// session without the caller needing the raw *http.Response.
func (r *Response) Cookies() []*http.Cookie {
	header := http.Header{}
	for _, value := range r.Header.Values("Set-Cookie") {
		header.Add("Set-Cookie", value)
	}
	return (&http.Response{Header: header}).Cookies()
}

// StatusError reports a response the caller asked to treat as a failure.
type StatusError struct {
	Service string
	Method  string
	URL     string
	Status  int
	Body    []byte
	Prefix  string
}

func (e *StatusError) Error() string {
	message := fmt.Sprintf("HTTP %d", e.Status)
	switch {
	case e.Prefix != "":
		return e.Prefix + message
	case e.Service != "":
		return e.Service + " 返回 " + message
	default:
		return message
	}
}

// Do performs the call and returns the buffered response. A non-2xx status is
// not an error here: RetryStatus decides whether it is retried and the caller
// decides whether it is a failure. Callers that only need "the body or a
// failure" want Read instead.
func (c *Client) Do(ctx context.Context, call Call) (*Response, error) {
	return c.attempt(ctx, call)
}

// Read performs the call and returns the body, turning a non-2xx response into
// a *StatusError.
func (c *Client) Read(ctx context.Context, call Call) ([]byte, error) {
	response, err := c.attempt(ctx, call)
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, statusErrorOf(call, response)
	}
	return response.Body, nil
}

// Stream performs the call and hands back the unread response so a caller can
// copy a large body to disk without buffering it. The caller owns Body and must
// close it; closing releases the attempt timeout.
func (c *Client) Stream(ctx context.Context, call Call) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	settings, err := c.policy()
	if err != nil {
		return nil, err
	}
	method := methodOf(call)
	attempts := attemptsFor(call, settings)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeoutFor(call, settings))
		request, err := newRequest(attemptCtx, method, call, settings)
		if err != nil {
			cancel()
			return nil, err
		}
		started := time.Now()
		response, err := settings.client.Do(request)
		if err != nil {
			cancel()
			lastErr = err
			c.record(call, method, 0, 0, attempt, started, err, settings)
			if doneErr := sleepBeforeRetry(ctx, attempt, attempts); doneErr != nil {
				return nil, doneErr
			}
			continue
		}
		if retryableStatus(call, response.StatusCode) && attempt < attempts {
			_ = response.Body.Close()
			cancel()
			lastErr = statusErrorOf(call, &Response{Status: response.StatusCode, Header: response.Header})
			c.record(call, method, response.StatusCode, 0, attempt, started, lastErr, settings)
			if doneErr := sleepBeforeRetry(ctx, attempt, attempts); doneErr != nil {
				return nil, doneErr
			}
			continue
		}
		c.record(call, method, response.StatusCode, 0, attempt, started, nil, settings)
		response.Body = &cancelOnClose{ReadCloser: response.Body, cancel: cancel}
		return response, nil
	}
	return nil, lastErr
}

func (c *Client) attempt(ctx context.Context, call Call) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	settings, err := c.policy()
	if err != nil {
		return nil, err
	}
	method := methodOf(call)
	attempts := attemptsFor(call, settings)
	maxBytes := countOr(call.MaxBytes, settings.maxBytes)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeoutFor(call, settings))
		request, err := newRequest(attemptCtx, method, call, settings)
		if err != nil {
			cancel()
			return nil, err
		}
		started := time.Now()
		response, err := settings.client.Do(request)
		if err != nil {
			cancel()
			lastErr = err
			c.record(call, method, 0, 0, attempt, started, err, settings)
			if doneErr := sleepBeforeRetry(ctx, attempt, attempts); doneErr != nil {
				return nil, doneErr
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxBytes))
		_ = response.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			c.record(call, method, response.StatusCode, 0, attempt, started, readErr, settings)
			if doneErr := sleepBeforeRetry(ctx, attempt, attempts); doneErr != nil {
				return nil, doneErr
			}
			continue
		}
		result := &Response{Status: response.StatusCode, Header: response.Header, Body: body}
		c.record(call, method, response.StatusCode, len(body), attempt, started, nil, settings)
		if retryableStatus(call, response.StatusCode) && attempt < attempts {
			lastErr = statusErrorOf(call, result)
			if doneErr := sleepBeforeRetry(ctx, attempt, attempts); doneErr != nil {
				return nil, doneErr
			}
			continue
		}
		return result, nil
	}
	return nil, lastErr
}

func newRequest(ctx context.Context, method string, call Call, settings policy) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, call.URL, bytes.NewReader(call.Body))
	if err != nil {
		return nil, err
	}
	for key, values := range call.Header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", settings.userAgent)
	}
	return request, nil
}

func statusErrorOf(call Call, response *Response) error {
	return &StatusError{
		Service: call.Service,
		Method:  methodOf(call),
		URL:     call.URL,
		Status:  response.Status,
		Body:    response.Body,
		Prefix:  call.StatusPrefix,
	}
}

// retryableStatus reports whether a failed response is worth another attempt. A
// successful response is never retried, whatever the policy says, so a policy
// like RetryAnyStatus cannot turn a good answer into three identical requests.
func retryableStatus(call Call, status int) bool {
	if succeeded(status) || call.RetryStatus == nil {
		return false
	}
	return call.RetryStatus(status)
}

func succeeded(status int) bool { return status >= 200 && status < 300 }

func attemptsFor(call Call, settings policy) int {
	if call.Attempts > 0 {
		return clampAttempts(call.Attempts)
	}
	return settings.attempts
}

func timeoutFor(call Call, settings policy) time.Duration {
	return durationOr(call.Timeout, settings.timeout)
}

func methodOf(call Call) string {
	method := strings.TrimSpace(call.Method)
	if method == "" {
		return http.MethodGet
	}
	return method
}

func clampAttempts(attempts int) int {
	if attempts < 1 {
		return 1
	}
	if attempts > maxAttempts {
		return maxAttempts
	}
	return attempts
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func countOr(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}
	return value
}

// sleepBeforeRetry pauses before the next attempt and returns the context error
// when the caller gave up first.
func sleepBeforeRetry(ctx context.Context, attempt, attempts int) error {
	if attempt >= attempts {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(attempt) * retryInterval):
		return nil
	}
}

// record writes one log line per attempt. Failures always get a line because a
// user cannot fix what they cannot see; successes are noise unless DEBUG is on.
func (c *Client) record(call Call, method string, status, size, attempt int, started time.Time, err error, settings policy) {
	if !settings.verbose && err == nil && status < 500 {
		return
	}
	logger := c.options.Logger
	if logger == nil {
		return
	}
	attrs := []any{
		"service", call.Service,
		"method", method,
		"target", logTarget(call.URL),
		"status", status,
		"bytes", size,
		"attempt", attempt,
		"elapsed", time.Since(started).Round(time.Millisecond).String(),
	}
	switch {
	case err != nil:
		logger.Warn("第三方请求失败", append(attrs, "error", err.Error())...)
	case status >= 500:
		logger.Warn("第三方请求返回错误状态", attrs...)
	default:
		logger.Debug("第三方请求完成", attrs...)
	}
}

// logTarget keeps the host and path so an operator can see where a call went,
// and drops the query string because API keys and tokens travel there.
func logTarget(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host + parsed.Path
}

// cancelOnClose releases the attempt timeout once the caller has read a stream.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// proxyKey identifies the proxy settings a transport was built for.
func proxyKey(options Options) string {
	if options.Proxy == nil {
		return "direct"
	}
	return options.Proxy.String() + "|" + options.ProxyList
}
