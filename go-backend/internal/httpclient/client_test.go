package httpclient_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/httpclient"
)

func TestShouldProxyMatchesExactAndSubdomains(t *testing.T) {
	target, _ := url.Parse("https://api.example.test/path")
	if !httpclient.ShouldProxy(target, "example.test\nother.test") {
		t.Fatal("subdomain was not matched")
	}
	target, _ = url.Parse("https://example.test/path")
	if !httpclient.ShouldProxy(target, "example.test") {
		t.Fatal("exact host was not matched")
	}
	target, _ = url.Parse("https://example.test.evil/path")
	if httpclient.ShouldProxy(target, "example.test") {
		t.Fatal("suffix without label was incorrectly matched")
	}
}

func TestClientUsesConfiguredProxyForMatchingHost(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoded := strings.TrimPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
		credentials, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(credentials) != "proxy-user:proxy-pass" {
			t.Fatalf("proxy auth = %q", r.Header.Get("Proxy-Authorization"))
		}
		if !strings.HasPrefix(r.URL.String(), "http://") {
			t.Fatalf("proxy did not receive absolute target URL: %s", r.URL)
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request bypassed configured proxy")
	}))
	defer target.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyPort, err := strconv.Atoi(proxyURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	client, err := httpclient.New(map[string]any{
		"proxy": true, "proxyHost": proxyURL.String(), "proxyPort": proxyPort,
		"proxyUsername": "proxy-user", "proxyPassword": "proxy-pass", "proxyList": "127.0.0.1",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "proxied" {
		t.Fatalf("proxy response = %q", body)
	}
}
