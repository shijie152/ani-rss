// Package httpclient centralizes timeout and selective proxy behavior for
// resource-source and metadata clients.
package httpclient

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func New(cfg model.Config, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if appconfig.Bool(cfg, "proxy") {
		proxy, err := appconfig.ProxyURL(cfg)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			transport.Proxy = func(request *http.Request) (*url.URL, error) {
				if ShouldProxy(request.URL, appconfig.String(cfg, "proxyList")) {
					return proxy, nil
				}
				return nil, nil
			}
		}
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

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
