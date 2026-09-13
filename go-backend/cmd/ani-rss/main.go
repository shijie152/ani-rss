package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
)

func main() {
	listenAddress := flag.String("listen", envOrDefault("LISTEN_ADDR", ":7789"), "public listen address")
	javaURL := flag.String("java-url", envOrDefault("JAVA_URL", "http://127.0.0.1:7790"), "Java transition backend URL")
	uiDirectory := flag.String("ui-dir", envOrDefault("UI_DIR", "ani-rss-ui/dist"), "directory containing the built Vue UI")
	configDirectory := flag.String("config-dir", envOrDefault("CONFIG", "config"), "directory containing ANI-RSS JSON data")
	goDomains := flag.String("go-domains", envOrDefault("GO_DOMAINS", "state,runtime,subscriptions,sources,rss,media"), "comma-separated business domains owned by Go")
	mcpEnabled := flag.Bool("mcp-enabled", envBoolOrDefault("MCP_ENABLED", false), "enable the MCP streamable HTTP endpoint")
	swaggerEnabled := flag.Bool("swagger-enabled", envBoolOrDefault("SWAGGER_ENABLED", false), "enable OpenAPI JSON and Swagger UI")
	flag.Parse()

	domains := splitDomains(*goDomains)
	app, err := backend.New(backend.Options{ConfigDir: *configDirectory, OwnershipDomains: domains, MCPEnabled: *mcpEnabled, SwaggerEnabled: *swaggerEnabled})
	if err != nil {
		slog.Error("Go backend initialization failed", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	ownedDomains := app.OwnedDomains()

	server := &http.Server{
		Addr: *listenAddress,
		Handler: gateway.New(gateway.Config{
			UIDirectory:     *uiDirectory,
			ConfigDirectory: *configDirectory,
			JavaURL:         *javaURL,
			GoRoutes:        app.Routes(),
			GoDomains:       ownedDomains,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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

	slog.Info("ANI-RSS Go Gateway listening", "address", *listenAddress, "java", *javaURL, "ui", *uiDirectory)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
	<-schedulerDone
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
