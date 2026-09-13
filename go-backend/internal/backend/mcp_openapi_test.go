package backend_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
	"github.com/shijie152/ani-rss/go-backend/internal/gateway"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestMCPFakeClientDiscoversAndCallsToolsWithAPIKey(t *testing.T) {
	app, server := newMCPTestServer(t, nil)
	defer server.Close()
	defer app.Close()

	initialize := mcpRequest(t, server.URL+"/api/mcp", "test-key", "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "fake", "version": "1"}},
	})
	if initialize.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", initialize.StatusCode)
	}
	sessionID := initialize.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize did not return a session id")
	}
	initializeBody := decodeBody(t, initialize)
	if initializeBody["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("protocol version = %#v", initializeBody)
	}

	listed := mcpRequest(t, server.URL+"/api/mcp", "test-key", sessionID, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	listBody := decodeBody(t, listed)
	tools := listBody["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 9 {
		t.Fatalf("tool count = %d", len(tools))
	}
	if tools[0].(map[string]any)["name"] != "list_subscriptions" {
		t.Fatalf("first tool = %#v", tools[0])
	}

	added := callJSONWithAPIKey(t, server.URL+"/api/addAni", "test-key", model.Ani{ID: "mcp-demo", Title: "MCP Demo", URL: "https://example.test/rss", Enable: true})
	if added["code"] != float64(http.StatusOK) {
		t.Fatalf("add subscription = %#v", added)
	}
	called := mcpRequest(t, server.URL+"/api/mcp", "test-key", sessionID, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_subscriptions", "arguments": map[string]any{"enabled": true}}})
	callBody := decodeBody(t, called)
	result := callBody["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("list tool error = %#v", result)
	}
	if !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "MCP Demo") {
		t.Fatalf("list tool result = %#v", result)
	}
}

func TestMCPRejectsMissingKeyAndReportsExternalFailureAsToolError(t *testing.T) {
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "upstream unavailable")
	}))
	defer external.Close()
	app, server := newMCPTestServer(t, external)
	defer server.Close()
	defer app.Close()

	unauthorized := mcpRequest(t, server.URL+"/api/mcp", "", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()

	initialized := mcpRequest(t, server.URL+"/api/mcp", "test-key", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	sessionID := initialized.Header.Get("Mcp-Session-Id")
	initialized.Body.Close()
	failed := mcpRequest(t, server.URL+"/api/mcp", "test-key", sessionID, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "search_mikan", "arguments": map[string]any{"text": "demo"}}})
	failedBody := decodeBody(t, failed)
	failedResult := failedBody["result"].(map[string]any)
	if failedResult["isError"] != true || !strings.Contains(failedResult["content"].([]any)[0].(map[string]any)["text"].(string), "source returned HTTP") {
		t.Fatalf("external failure = %#v", failedBody)
	}
}

func TestMCPSupportsServerSentEventResponse(t *testing.T) {
	app, server := newMCPTestServer(t, nil)
	defer server.Close()
	defer app.Close()
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}
	body, _ := json.Marshal(request)
	httpRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/mcp", bytes.NewReader(body))
	httpRequest.Header.Set("api-key", "test-key")
	httpRequest.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(data), "event: message") || !strings.Contains(string(data), `"protocolVersion"`) {
		t.Fatalf("SSE response: content-type=%q body=%s", response.Header.Get("Content-Type"), data)
	}
}

func TestOpenAPIDescribesEveryGoRouteAndAuthContract(t *testing.T) {
	app, server := newMCPTestServer(t, nil)
	defer server.Close()
	defer app.Close()
	response, err := http.Get(server.URL + "/v3/api-docs")
	if err != nil {
		t.Fatal(err)
	}
	document := decodeBody(t, response)
	paths := document["paths"].(map[string]any)
	for _, route := range app.Routes() {
		if strings.HasPrefix(route.Path, "/v3/") || strings.HasPrefix(route.Path, "/swagger") {
			continue
		}
		pathItem, ok := paths[route.Path].(map[string]any)
		if !ok {
			t.Fatalf("route missing from OpenAPI: %s %s", route.Method, route.Path)
		}
		operation, ok := pathItem[strings.ToLower(route.Method)].(map[string]any)
		if !ok {
			t.Fatalf("method missing from OpenAPI: %s %s", route.Method, route.Path)
		}
		if operation["responses"] == nil {
			t.Fatalf("responses missing for %s %s", route.Method, route.Path)
		}
		if operation["operationId"] == nil || operation["summary"] == nil {
			t.Fatalf("operation metadata missing for %s %s", route.Method, route.Path)
		}
	}
	if document["openapi"] != "3.0.3" {
		t.Fatalf("openapi version = %#v", document["openapi"])
	}
	components := document["components"].(map[string]any)
	security := components["securitySchemes"].(map[string]any)["api-key"].(map[string]any)
	if security["in"] != "header" || security["name"] != "api-key" {
		t.Fatalf("api-key scheme = %#v", security)
	}
	for _, path := range []string{"/api/mikan", "/api/mikanGroup", "/api/getSubtitles", "/api/file"} {
		if paths[path] == nil {
			t.Fatalf("important path absent: %s", path)
		}
	}
	mikanOperation := paths["/api/mikan"].(map[string]any)["post"].(map[string]any)
	if len(mikanOperation["parameters"].([]any)) != 1 || mikanOperation["requestBody"] == nil || mikanOperation["security"] == nil {
		t.Fatalf("mikan contract metadata = %#v", mikanOperation)
	}
	uploadOperation := paths["/api/upload"].(map[string]any)["post"].(map[string]any)
	if !strings.Contains(jsonString(uploadOperation["requestBody"]), "multipart/form-data") {
		t.Fatalf("upload is not documented as multipart: %#v", uploadOperation["requestBody"])
	}
	response.Body.Close()

	uiResponse, err := http.Get(server.URL + "/swagger-ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	uiBody, _ := io.ReadAll(uiResponse.Body)
	uiResponse.Body.Close()
	if uiResponse.StatusCode != http.StatusOK || !strings.Contains(string(uiBody), "/v3/api-docs") {
		t.Fatalf("swagger UI status/body = %d/%s", uiResponse.StatusCode, uiBody)
	}
}

func jsonString(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func newMCPTestServer(t *testing.T, external *httptest.Server) (*backend.App, *httptest.Server) {
	t.Helper()
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir(), Version: "test", MCPEnabled: true, SwaggerEnabled: true, OwnershipDomains: []string{"runtime", "subscriptions", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Config().Update(model.Config{"apiKey": "test-key"}); err != nil {
		app.Close()
		t.Fatal(err)
	}
	if external != nil {
		if err := app.Config().Update(model.Config{"mikanHost": external.URL}); err != nil {
			app.Close()
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(gateway.New(gateway.Config{GoRoutes: app.Routes(), GoDomains: []string{"runtime", "subscriptions", "sources"}}))
	return app, server
}

func mcpRequest(t *testing.T, endpoint, key, session string, payload map[string]any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		request.Header.Set("api-key", key)
	}
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeBody(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func callJSONWithAPIKey(t *testing.T, target, key string, body any) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("api-key", key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
