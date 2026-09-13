// Package gateway owns the public HTTP entry point during the Java-to-Go migration.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Route is a Go-owned public route. Routes not in this table are sent to Java.
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
	JavaURL         string
	GoRoutes        []Route
	GoDomains       []string
}

// Gateway serves the existing UI and chooses between Go-owned and Java-owned
// HTTP routes. SetGoRoutes is safe to call while requests are being served.
type Gateway struct {
	uiDirectory     string
	configDirectory string
	javaProxy       http.Handler

	routesMu   sync.RWMutex
	routes     map[string]route
	domains    map[string]struct{}
	domainsSet bool
}

type route struct {
	domain  string
	handler http.Handler
}

// New creates a Gateway. An empty JavaURL is allowed so the gateway can be
// used while no fallback backend is configured; API requests then receive a
// UI-compatible 502 response.
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

	if config.JavaURL != "" {
		if target, err := url.Parse(config.JavaURL); err == nil && isHTTPURL(target) {
			gateway.javaProxy = newJavaProxy(target)
		} else {
			gateway.javaProxy = unavailableBackend("invalid Java backend URL")
		}
	} else {
		gateway.javaProxy = unavailableBackend("Java backend is not configured")
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
// Passing nil disables all registered routes and restores the Java fallback.
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

// ServeHTTP is the public seam for the Gateway. API requests are dispatched
// to Go when owned, and otherwise forwarded to Java. All other requests are
// served from the configured UI directory with SPA fallback.
func (gateway *Gateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if isAPIRequest(request.URL.Path) {
		if handler := gateway.goRoute(request.Method, request.URL.Path); handler != nil {
			handler.ServeHTTP(response, request)
			return
		}
		gateway.javaProxy.ServeHTTP(response, request)
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

func newJavaProxy(target *url.URL) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(response http.ResponseWriter, _ *http.Request, _ error) {
		writeGatewayError(response, http.StatusBadGateway, "Java backend is unavailable")
	}
	return proxy
}

func unavailableBackend(message string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeGatewayError(response, http.StatusBadGateway, message)
	})
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

func isHTTPURL(target *url.URL) bool {
	return (target.Scheme == "http" || target.Scheme == "https") && target.Host != ""
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
