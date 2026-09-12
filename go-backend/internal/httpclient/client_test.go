package httpclient_test

import (
	"net/url"
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
