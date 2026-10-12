package outbound

import (
	"log/slog"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// FromConfig builds a client whose policy comes from the given snapshot, and
// reports a proxy that cannot be used before any request is made. Handlers that
// should fail before doing work use it; per-request callers already hold a
// snapshot and reach for it too.
func FromConfig(cfg model.Config, logger *slog.Logger) (*Client, error) {
	if _, err := appconfig.ProxyURL(cfg); err != nil {
		return nil, err
	}
	return Watch(func() model.Config { return cfg }, logger), nil
}

// Watch builds a client whose policy follows whatever snapshot snapshots
// returns. Components that live as long as the process use it, so a proxy the
// user turns on in settings takes effect without restarting the service. Every
// third-party call in the service takes its client from here, which is what
// makes the configured proxy and timeout apply to all of them.
func Watch(snapshots func() model.Config, logger *slog.Logger) *Client {
	return &Client{snapshot: snapshots, options: Options{Logger: logger}}
}

// following returns the options a snapshot implies, keeping the explicit
// settings the caller supplied for anything the configuration does not own.
func (o Options) following(cfg model.Config) (Options, error) {
	proxy, err := appconfig.ProxyURL(cfg)
	if err != nil {
		// The error reaches the caller of the request that could not be made,
		// so an unusable proxy reports itself instead of silently going direct.
		return Options{}, err
	}
	o.Proxy = proxy
	o.ProxyList = appconfig.String(cfg, "proxyList")
	o.Timeout = time.Duration(appconfig.Int(cfg, "rssTimeout")) * time.Second
	o.Verbose = appconfig.Bool(cfg, "debug")
	return o, nil
}
