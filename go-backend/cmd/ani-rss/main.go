package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/shijie152/ani-rss/go-backend/internal/bootstrap"
	"github.com/shijie152/ani-rss/go-backend/internal/desktop"
)

// version is injected by the release build. The development value keeps local
// binaries and diagnostics self-describing.
var version = "dev"

func main() {
	listenAddress := flag.String("listen", envOrDefault("LISTEN_ADDR", ":7789"), "public listen address")
	uiDirectory := flag.String("ui-dir", envOrDefault("UI_DIR", "ani-rss-ui/dist"), "directory containing the built Vue UI")
	configDirectory := flag.String("config-dir", envOrDefault("CONFIG", "config"), "directory containing ANI-RSS JSON data")
	goDomains := flag.String("go-domains", envOrDefault("GO_DOMAINS", "state,runtime,subscriptions,sources,rss,media,rename,maintenance"), "comma-separated business domains owned by Go")
	gui := flag.Bool("gui", envBoolOrDefault("GUI", false), "enable the native desktop tray on macOS or Windows")
	mcpEnabled := flag.Bool("mcp-enabled", envBoolOrDefault("MCP_ENABLED", false), "enable the MCP streamable HTTP endpoint")
	swaggerEnabled := flag.Bool("swagger-enabled", envBoolOrDefault("SWAGGER_ENABLED", false), "enable OpenAPI JSON and Swagger UI")
	flag.Parse()

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restartRequested atomic.Bool
	// 真实监听地址：端口写 0 时 flag 里的值不是实际端口，托盘与日志都要用它。
	boundAddress := make(chan string, 1)
	// 装配、生命周期与重启都归 bootstrap：这里只读选项。
	service, err := bootstrap.New(bootstrap.Options{
		ConfigDir:        *configDirectory,
		UIDirectory:      *uiDirectory,
		ListenAddress:    *listenAddress,
		Version:          version,
		UpdateEndpoint:   envOrDefault("UPDATE_API_URL", "https://api.github.com/repos/wushuo894/ani-rss/releases/latest"),
		OwnershipDomains: splitDomains(*goDomains),
		MCPEnabled:       *mcpEnabled,
		SwaggerEnabled:   *swaggerEnabled,
		OnShutdown: func(restart bool) {
			restartRequested.Store(restart)
			if *gui {
				desktop.Stop()
			}
			stop()
		},
		// 端口为 0 时 flag 里的地址不是真实监听地址，用实际绑定的那个。
		OnListening: func(address string) {
			slog.Info("ANI-RSS Go service listening", "address", address, "ui", *uiDirectory)
			select {
			case boundAddress <- address:
			default:
			}
		},
	})
	if err != nil {
		slog.Error("Go backend initialization failed", "error", err)
		os.Exit(1)
	}
	defer service.App().Close()

	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(shutdownContext) }()
	if *gui {
		address := *listenAddress
		select {
		case address = <-boundAddress:
		case <-shutdownContext.Done():
		}
		desktop.Start(address, *configDirectory, *uiDirectory)
		stop()
	}
	if err := <-runDone; err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
	if restartRequested.Load() {
		if err := service.Restart(context.Background()); err != nil {
			slog.Error("restart failed", "error", err)
		}
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func splitDomains(value string) []string {
	parts := strings.Split(value, ",")
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		if domain := strings.TrimSpace(part); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}

func envBoolOrDefault(name string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
