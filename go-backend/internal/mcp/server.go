// Package mcp implements the small, stateless part of the MCP streamable HTTP
// transport used by ANI-RSS. Keeping the transport here avoids coupling the
// application to a particular MCP SDK while preserving the wire contract
// consumed by current MCP clients.
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const (
	latestProtocol = "2025-06-18"
	serverName     = "ani-rss"
)

// Tool is one MCP tool exposed by the application.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(context.Context, json.RawMessage) (any, error)
}

// Config configures the HTTP transport and its application tools.
type Config struct {
	Version   string
	Authorize func(*http.Request) bool
	Tools     []Tool
}

type Server struct {
	version   string
	authorize func(*http.Request) bool
	tools     map[string]Tool
	sessions  map[string]struct{}
	mu        sync.RWMutex
}

func New(config Config) *Server {
	tools := make(map[string]Tool, len(config.Tools))
	for _, tool := range config.Tools {
		if tool.Name != "" && tool.Call != nil {
			if tool.InputSchema == nil {
				tool.InputSchema = ObjectSchema(nil)
			}
			tools[tool.Name] = tool
		}
	}
	return &Server{version: config.Version, authorize: config.Authorize, tools: tools, sessions: make(map[string]struct{})}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		if r.Method == http.MethodGet {
			writeJSONRPCError(w, nil, -32000, "MCP stream is established with POST")
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.authorize != nil && !s.authorize(r) {
		writeJSONRPCErrorStatus(w, nil, -32001, "API key required", http.StatusForbidden)
		return
	}

	var request rpcRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	if err := decoder.Decode(&request); err != nil {
		writeJSONRPCError(w, nil, -32700, "Parse error")
		return
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		writeJSONRPCError(w, request.ID, -32600, "Invalid Request")
		return
	}

	if request.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if request.Method == "initialize" {
		sessionID := newSessionID()
		s.mu.Lock()
		s.sessions[sessionID] = struct{}{}
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", sessionID)
		writeMessage(w, r, request.ID, s.initialize(request.Params))
		return
	}

	if sessionID := strings.TrimSpace(r.Header.Get("Mcp-Session-Id")); sessionID != "" {
		s.mu.RLock()
		_, valid := s.sessions[sessionID]
		s.mu.RUnlock()
		if !valid {
			writeJSONRPCError(w, request.ID, -32002, "Unknown MCP session")
			return
		}
		w.Header().Set("Mcp-Session-Id", sessionID)
	}

	response, isError := s.dispatch(r.Context(), request)
	if isError {
		writeJSONRPCError(w, request.ID, response.code, response.message)
		return
	}
	writeMessage(w, r, request.ID, response.result)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type dispatchResponse struct {
	result  any
	code    int
	message string
}

func (s *Server) initialize(rawParams json.RawMessage) any {
	version := latestProtocol
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(rawParams) > 0 {
		_ = json.Unmarshal(rawParams, &params)
	}
	switch params.ProtocolVersion {
	case "2024-11-05", "2025-03-26", "2025-06-18":
		version = params.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": serverName, "version": s.version},
		"instructions":    "使用 ANI-RSS 工具搜索资源、预览并管理订阅。",
	}
}

func (s *Server) dispatch(ctx context.Context, request rpcRequest) (dispatchResponse, bool) {
	switch request.Method {
	case "ping":
		return dispatchResponse{result: map[string]any{}}, false
	case "tools/list":
		return dispatchResponse{result: s.listTools()}, false
	case "tools/call":
		return s.callTool(ctx, request.Params)
	default:
		return dispatchResponse{code: -32601, message: "Method not found"}, true
	}
}

func (s *Server) listTools() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]map[string]any, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema})
	}
	// Tool order is part of the human-facing API. The application registers
	// them in the Java order and this stable order also makes fake-client tests
	// deterministic.
	order := []string{"list_subscriptions", "search_mikan", "search_anibt", "search_anime_garden", "get_mikan_groups", "get_anibt_groups", "get_anime_garden_groups", "preview_subscription_items", "add_subscription"}
	ordered := make([]map[string]any, 0, len(tools))
	for _, name := range order {
		if tool, ok := s.tools[name]; ok {
			ordered = append(ordered, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema})
		}
	}
	return map[string]any{"tools": ordered}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (dispatchResponse, bool) {
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(params) == 0 || json.Unmarshal(params, &input) != nil || strings.TrimSpace(input.Name) == "" {
		return dispatchResponse{code: -32602, message: "Invalid tool arguments"}, true
	}
	s.mu.RLock()
	tool, ok := s.tools[input.Name]
	s.mu.RUnlock()
	if !ok {
		return dispatchResponse{code: -32602, message: "Unknown tool: " + input.Name}, true
	}
	if len(input.Arguments) == 0 {
		input.Arguments = json.RawMessage(`{}`)
	}
	result, err := tool.Call(ctx, input.Arguments)
	if err != nil {
		// Tool execution failures are tool results, rather than JSON-RPC
		// transport failures, as required by MCP clients.
		return dispatchResponse{result: map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}}, false
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return dispatchResponse{result: map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}}, false
	}
	return dispatchResponse{result: map[string]any{"content": []map[string]any{{"type": "text", "text": string(encoded)}}, "structuredContent": result}}, false
}

func writeMessage(w http.ResponseWriter, r *http.Request, id json.RawMessage, result any) {
	payload := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
	data, err := json.Marshal(payload)
	if err != nil {
		writeJSONRPCError(w, id, -32603, "Internal error")
		return
	}
	if wantsSSE(r) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSONRPCErrorStatus(w, id, code, message, http.StatusOK)
}

func writeJSONRPCErrorStatus(w http.ResponseWriter, id json.RawMessage, code int, message string, status int) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func wantsSSE(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/event-stream") && !strings.Contains(accept, "application/json")
}

func ObjectSchema(properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
}

func newSessionID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "ani-rss-session"
	}
	return hex.EncodeToString(value)
}

func StringArg(arguments json.RawMessage, name string, required bool) (string, error) {
	var values map[string]json.RawMessage
	if len(arguments) == 0 || json.Unmarshal(arguments, &values) != nil {
		return "", errors.New("arguments must be an object")
	}
	var value string
	if raw, ok := values[name]; ok {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("%s must be a string", name)
		}
	}
	if required && strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
