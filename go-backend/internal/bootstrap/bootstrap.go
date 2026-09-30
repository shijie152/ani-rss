// Package bootstrap owns process-level assembly and lifecycle: it builds the
// backend and gateway from options, runs the schedulers for the domains this
// process owns, shuts everything down in one order, and restarts the binary.
// main and the update handler both go through here instead of repeating the
// sequence themselves.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
)

// Options are the process-level inputs: what to serve and which domains this
// process owns.
type Options struct {
	ConfigDir        string
	UIDirectory      string
	ListenAddress    string
	Version          string
	UpdateEndpoint   string
	OwnershipDomains []string
	MCPEnabled       bool
	SwaggerEnabled   bool
	// OnShutdown runs before the process releases its resources, e.g. to stop
	// a desktop tray or cancel the signal context.
	OnShutdown func(restart bool)
	// OnRestart runs right before the replacement process starts.
	OnRestart func()
	// OnListening reports the bound address once Run is accepting requests.
	OnListening func(address string)
	// ResolveExecutable names the binary to start on restart. Tests inject a
	// harmless command instead of spawning a second ANI-RSS process.
	ResolveExecutable func() (string, []string, error)
}

// Service is a running (or ready to run) ANI-RSS process.
type Service struct {
	options    Options
	app        *backend.App
	server     *http.Server
	listener   net.Listener
	address    string
	executable func() (string, []string, error)
	ready      chan struct{}
}

// New assembles the backend and gateway. No port is bound until Run.
func New(options Options) (*Service, error) {
	service := &Service{options: options, ready: make(chan struct{})}
	service.executable = options.ResolveExecutable
	if service.executable == nil {
		service.executable = func() (string, []string, error) {
			path, err := os.Executable()
			if err != nil {
				return "", nil, err
			}
			return path, os.Args[1:], nil
		}
	}
	app, err := backend.New(backend.Options{
		ConfigDir:        options.ConfigDir,
		Version:          options.Version,
		UpdateEndpoint:   options.UpdateEndpoint,
		OwnershipDomains: options.OwnershipDomains,
		MCPEnabled:       options.MCPEnabled,
		SwaggerEnabled:   options.SwaggerEnabled,
		Restart:          service.Restart,
		Shutdown: func(restart bool) {
			if options.OnShutdown != nil {
				options.OnShutdown(restart)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	service.app = app
	service.server = &http.Server{
		Addr: options.ListenAddress,
		Handler: gateway.New(gateway.Config{
			UIDirectory:     options.UIDirectory,
			ConfigDirectory: options.ConfigDir,
			GoRoutes:        app.Routes(),
			GoDomains:       app.OwnedDomains(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return service, nil
}

// App exposes the assembled backend for callers that need its routes.
func (s *Service) App() *backend.App { return s.app }

// Run serves until ctx is cancelled or Shutdown is called, then releases
// everything in one order: gateway, schedulers, backend resources.
func (s *Service) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.listener = listener
	s.address = listener.Addr().String()
	close(s.ready)
	if s.options.OnListening != nil {
		s.options.OnListening(s.address)
	}

	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		s.app.RunSchedulers(ctx)
	}()

	serveDone := make(chan error, 1)
	go func() { serveDone <- s.server.Serve(listener) }()

	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.app.Close()
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			slog.Error("gateway shutdown failed", "error", err)
		}
		cancel()
	}
	<-schedulerDone
	s.app.Close()
	return nil
}

// Restart releases this process's resources, then starts a replacement with
// the same arguments. The replacement starts only after the ownership lock is
// released, so it can claim the same domains.
func (s *Service) Restart(ctx context.Context) error {
	if s.options.OnRestart != nil {
		s.options.OnRestart()
	}
	// 替代进程会用 O_EXCL 抢同一批 domain 锁；本进程必须先释放，否则新进程
	// 一启动就因「domain 已被占用」而失败。Close 可重复调用，调用方的 defer
	// 仍然安全。
	s.app.Close()
	path, args, err := s.executable()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Stdout, command.Stderr, command.Stdin = os.Stdout, os.Stderr, os.Stdin
	return command.Start()
}
