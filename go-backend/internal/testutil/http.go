// Package testutil contains deterministic helpers shared by integration tests.
package testutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// InstallLoopbackNetworkGuard makes every client based on http.DefaultTransport
// reject non-loopback requests. Call it from a package's TestMain or test init
// so production clients created by the application inherit the guard too.
func InstallLoopbackNetworkGuard() {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("test network isolation: unexpected default HTTP transport")
	}
	guarded := transport.Clone()
	// Do not let an environment-configured proxy turn an external URL into a
	// loopback dial that would bypass the target-host check below.
	guarded.Proxy = nil
	baseDialContext := guarded.DialContext
	guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !isLoopbackHost(host) {
			return nil, fmt.Errorf("test network isolation: dial must target loopback")
		}
		if baseDialContext != nil {
			return baseDialContext(ctx, network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = guarded
}

// InstallTestNetworkGuard installs the loopback-only guard for a deterministic
// test package. Live tests are separated with the live build tag, so an
// environment variable can never silently disable ordinary test isolation.
func InstallTestNetworkGuard() {
	InstallLoopbackNetworkGuard()
}

// LocalHTTPClient returns a bounded client that can only reach loopback test
// servers. It prevents a test fixture from accidentally depending on a live
// third-party service.
func LocalHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: localOnlyTransport{base: http.DefaultTransport},
	}
}

type localOnlyTransport struct {
	base http.RoundTripper
}

func (t localOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || !isLoopbackHost(request.URL.Hostname()) {
		return nil, fmt.Errorf("test network isolation: target must be loopback")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(request)
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
