package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
)

func main() {
	listenAddress := flag.String("listen", envOrDefault("LISTEN_ADDR", ":7789"), "public listen address")
	javaURL := flag.String("java-url", envOrDefault("JAVA_URL", "http://127.0.0.1:7790"), "Java transition backend URL")
	uiDirectory := flag.String("ui-dir", envOrDefault("UI_DIR", "ani-rss-ui/dist"), "directory containing the built Vue UI")
	flag.Parse()

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           gateway.New(gateway.Config{UIDirectory: *uiDirectory, JavaURL: *javaURL}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
