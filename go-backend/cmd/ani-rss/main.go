package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/desktop"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
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
	domains := splitDomains(*goDomains)
	app, err := backend.New(backend.Options{ConfigDir: *configDirectory, Version: version, UpdateEndpoint: envOrDefault("UPDATE_API_URL", "https://api.github.com/repos/wushuo894/ani-rss/releases/latest"), Shutdown: func(restart bool) {
		restartRequested.Store(restart)
		if *gui {
			desktop.Stop()
		}
		stop()
	}, OwnershipDomains: domains, MCPEnabled: *mcpEnabled, SwaggerEnabled: *swaggerEnabled})
	if err != nil {
		slog.Error("Go backend initialization failed", "error", err)
		os.Exit(1)
	}
	defer app.Close()

	server := &http.Server{
		Addr: *listenAddress,
		Handler: gateway.New(gateway.Config{
			UIDirectory:     *uiDirectory,
			ConfigDirectory: *configDirectory,
			GoRoutes:        app.Routes(),
			GoDomains:       app.OwnedDomains(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		app.RunSchedulers(shutdownContext)
	}()

	go func() {
		<-shutdownContext.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("gateway shutdown failed", "error", err)
		}
	}()

	slog.Info("ANI-RSS Go service listening", "address", *listenAddress, "ui", *uiDirectory)
	if *gui {
		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("gateway stopped", "error", err)
			}
		}()
		desktop.Start(*listenAddress, *configDirectory, *uiDirectory)
		stop()
		<-schedulerDone
	} else {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("gateway stopped", "error", err)
			os.Exit(1)
		}
		<-schedulerDone
	}
	if restartRequested.Load() {
		// Release the ownership lock before starting the replacement process.
		// The deferred Close remains safe and handles the ordinary exit path.
		app.Close()
		if executable, executableErr := os.Executable(); executableErr == nil {
			command := exec.Command(executable, os.Args[1:]...)
			command.Stdout, command.Stderr, command.Stdin = os.Stdout, os.Stderr, os.Stdin
			if startErr := command.Start(); startErr != nil {
				slog.Error("restart failed", "error", startErr)
			}
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
