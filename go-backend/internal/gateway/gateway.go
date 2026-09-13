// Package gateway owns the public HTTP entry point for the Go service.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Route is a public route served by the Go service.
type Route struct {
	Domain  string
	Method  string
	Path    string
	Handler http.Handler
}

// Config configures the migration gateway.
type Config struct {
	UIDirectory     string
	ConfigDirectory string
	GoRoutes        []Route
	GoDomains       []string
}

// Gateway serves the existing UI and dispatches API routes to the Go backend.
// SetGoRoutes is safe to call while requests are being served.
type Gateway struct {
	uiDirectory     string
	configDirectory string
	routesMu        sync.RWMutex
	routes          map[string]route
	domains         map[string]struct{}
	domainsSet      bool
}

type route struct {
	domain  string
	handler http.Handler
}

// New creates a Go-only gateway. API paths not registered in Go return a
// UI-compatible 404 response; no secondary runtime is contacted.
func New(config Config) *Gateway {
	gateway := &Gateway{
		uiDirectory:     config.UIDirectory,
		configDirectory: config.ConfigDirectory,
		routes:          make(map[string]route),
	}
	gateway.SetGoRoutes(config.GoRoutes)
	if config.GoDomains != nil {
		gateway.SetGoDomains(config.GoDomains)
	}

	return gateway
}

// SetGoRoutes atomically replaces the routes registered with Go. A route is
// selected by HTTP method and case-insensitive URL path. When a domain policy
// is active, the route's domain must also be enabled.
func (gateway *Gateway) SetGoRoutes(routes []Route) {
	next := make(map[string]route, len(routes))
	for _, definition := range routes {
		if definition.Handler == nil {
			continue
		}
		next[routeKey(definition.Method, definition.Path)] = route{
			domain:  definition.Domain,
			handler: definition.Handler,
		}
	}

	gateway.routesMu.Lock()
	gateway.routes = next
	gateway.routesMu.Unlock()
}

// SetGoDomains atomically changes which business domains are owned by Go.
// Passing nil disables all registered routes. This is useful for testing and
// for an operator-controlled domain maintenance window.
// A Gateway created without GoDomains keeps all registered routes active,
// which is convenient for a single-runtime deployment.
func (gateway *Gateway) SetGoDomains(domains []string) {
	active := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		if domain != "" {
			active[domain] = struct{}{}
		}
	}

	gateway.routesMu.Lock()
	gateway.domains = active
	gateway.domainsSet = true
	gateway.routesMu.Unlock()
}

// ServeHTTP is the public seam for the Go service. API requests are dispatched
// to a registered Go handler; all other requests are served from the UI.
func (gateway *Gateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if isAPIRequest(request.URL.Path) {
		if handler := gateway.goRoute(request.Method, request.URL.Path); handler != nil {
			handler.ServeHTTP(response, request)
			return
		}
		writeGatewayError(response, http.StatusNotFound, "接口不存在")
		return
	}

	gateway.serveUI(response, request)
}

func (gateway *Gateway) goRoute(method, path string) http.Handler {
	gateway.routesMu.RLock()
	route, ok := gateway.routes[routeKey(method, path)]
	if ok && gateway.domainsSet {
		_, ok = gateway.domains[route.domain]
	}
	gateway.routesMu.RUnlock()
	if !ok {
		return nil
	}
	return route.handler
}

func (gateway *Gateway) serveUI(response http.ResponseWriter, request *http.Request) {
	uiDirectory := gateway.uiDirectory
	if gateway.configDirectory != "" {
		custom := filepath.Join(gateway.configDirectory, "webui")
		if isRegularFile(filepath.Join(custom, "index.html")) {
			uiDirectory = custom
		}
	}
	if uiDirectory == "" {
		http.NotFound(response, request)
		return
	}

	root, err := filepath.Abs(uiDirectory)
	if err != nil {
		http.Error(response, "UI directory is unavailable", http.StatusInternalServerError)
		return
	}

	requested, safe := uiPath(root, request.URL.Path)
	if safe && isRegularFile(requested) {
		serveUIFile(response, request, requested)
		return
	}

	// Vue history-mode routes must render the same index document as "/".
	// Asset requests are not eligible for fallback and remain ordinary 404s.
	if (request.URL.Path == "/" || (acceptsHTML(request) && filepath.Ext(request.URL.Path) == "")) && isRegularFile(filepath.Join(root, "index.html")) {
		serveUIFile(response, request, filepath.Join(root, "index.html"))
		return
	}

	http.NotFound(response, request)
}

func writeGatewayError(response http.ResponseWriter, status int, message string) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Time    int64  `json:"t"`
	}{
		Code:    status,
		Message: message,
		Time:    time.Now().UnixMilli(),
	})
}

func isAPIRequest(path string) bool {
	path = strings.ToLower(path)
	return path == "/api" || strings.HasPrefix(path, "/api/") || path == "/v3/api-docs" || path == "/swagger-ui.html" || path == "/swagger-ui/index.html"
}

func routeKey(method, path string) string {
	return strings.ToUpper(method) + " " + strings.ToLower(path)
}

func acceptsHTML(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Accept")), "text/html")
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func serveUIFile(response http.ResponseWriter, request *http.Request, path string) {
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(response, request)
		return
	}
	http.ServeContent(response, request, filepath.Base(path), info.ModTime(), file)
}

func uiPath(root, requestPath string) (string, bool) {
	relative := filepath.FromSlash(strings.TrimPrefix(requestPath, "/"))
	joined := filepath.Join(root, relative)
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(joined)
	if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
		return "", false
	}
	return cleanPath, true
}

var _ http.Handler = (*Gateway)(nil)
