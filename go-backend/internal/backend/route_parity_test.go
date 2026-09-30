package backend

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 前端端点表（ani-rss-ui/src/js/endpoints.js）是 UI 契约的唯一真相源；它的
// 每条路径与方法都必须与后端路由表一致，否则 UI 会在运行时才 404。
// ADR-0001 要求 UI 契约不变，这条测试是那个不变量的守卫。
var (
	uiEndpointPattern = regexp.MustCompile(`(\w+): endpoint\('([^']+)'(?:, \{method: '(\w+)')?`)
	// 路由注册有多种写法（helper 调用、Route 结构体字面量，方法名在前或在后），
	// 这里按「同一条注册里同时出现 Method 与 Path」提取，不绑定顺序。
	// 路径可能含多级斜杠（如 /api/bgm/oauth/callback）。
	// 路径可含多级斜杠与点号（/api/bgm/oauth/callback、/api/calendar.ics）。
	goRoutePattern = regexp.MustCompile(`(?m)http\.Method(\w+)[^\n]*?"(/api/[A-Za-z0-9/._-]+)"|"(/api/[A-Za-z0-9/._-]+)"[^\n]*?http\.Method(\w+)`)
)

func TestUIEndpointTableMatchesGoRoutes(t *testing.T) {
	root := filepath.Join("..", "..", "..", "ani-rss-ui", "src", "js", "endpoints.js")
	table, err := os.ReadFile(root)
	if err != nil {
		// 端点表是这条不变量的一半，读不到就必须失败：跳过会让守卫静默消失。
		t.Fatalf("UI endpoint table missing at %s: %v", root, err)
	}
	routes, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}

	declared := map[string]string{}
	for _, match := range uiEndpointPattern.FindAllStringSubmatch(string(table), -1) {
		method := match[3]
		if method == "" {
			method = http.MethodPost
		}
		declared[strings.TrimPrefix(match[2], "/")] = method
	}
	if len(declared) == 0 {
		t.Fatal("no endpoints parsed from the UI table")
	}
	// 解析器漏项会让这条测试静默变弱：UI 表里 endpoint(...) 的出现次数必须
	// 与解析出的条数一致。
	if occurrences := strings.Count(string(table), "endpoint('api/"); occurrences != len(declared) {
		t.Fatalf("parsed %d endpoints but the table declares %d", len(declared), occurrences)
	}

	served := map[string][]string{}
	for _, match := range goRoutePattern.FindAllStringSubmatch(string(routes), -1) {
		method, path := match[1], match[2]
		if method == "" {
			method, path = match[4], match[3]
		}
		path = strings.TrimPrefix(path, "/")
		served[path] = append(served[path], method)
	}

	for path, method := range declared {
		methods, ok := served[path]
		if !ok {
			t.Errorf("UI calls %s but the Go backend serves no such route", path)
			continue
		}
		found := false
		for _, served := range methods {
			if strings.EqualFold(served, method) {
				found = true
			}
		}
		if !found {
			t.Errorf("UI calls %s %s but the Go backend serves %v", method, path, methods)
		}
	}
}
