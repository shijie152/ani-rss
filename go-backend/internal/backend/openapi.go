package backend

import (
	"encoding/json"
	"net/http"
	"strings"
)

var openAPISummaries = map[string]string{
	"/api/ping": "健康检查", "/api/login": "登录", "/api/config": "获取公开配置", "/api/setConfig": "更新配置",
	"/api/listAni": "订阅列表", "/api/addAni": "添加订阅", "/api/setAni": "修改订阅", "/api/deleteAni": "删除订阅",
	"/api/mikan": "获取 Mikan 番剧列表", "/api/mikanGroup": "获取 Mikan 字幕组", "/api/aniBT": "AniBT 番剧列表",
	"/api/aniBTGroup": "获取 AniBT 字幕组", "/api/animeGardenList": "AnimeGarden 番剧列表", "/api/animeGardenGroup": "AnimeGarden 字幕组",
	"/api/searchBgm": "搜索 Bangumi", "/api/getAniBySubjectId": "根据 Bangumi ID 创建订阅", "/api/getBgmTitle": "获取 Bangumi 标题",
	"/api/rate": "获取 Bangumi 评分", "/api/setRate": "保存 Bangumi 评分", "/api/meBgm": "获取 Bangumi 账号信息", "/api/bgm/oauth/callback": "Bangumi OAuth 回调",
	"/api/rssToAni": "将 RSS 转为订阅", "/api/refreshAll": "刷新全部订阅", "/api/refreshAni": "刷新订阅",
	"/api/previewAni": "预览订阅", "/api/torrentsInfos": "下载任务列表", "/api/startCollection": "开始合集下载",
	"/api/previewCollection": "预览合集", "/api/getCollectionSubgroup": "获取合集字幕组", "/api/scrape": "刮削订阅",
	"/api/batchScrape": "批量刮削", "/api/playList": "播放列表", "/api/getSubtitles": "获取字幕",
	"/api/file": "读取媒体文件", "/api/upload": "上传文件", "/api/uploadAndRead": "上传并读取文件",
	"/api/uploadAndReadToBase64": "上传并读取 Base64", "/api/exportConfig": "导出配置备份", "/api/importConfig": "导入配置备份",
	"/api/mcp": "MCP Streamable HTTP endpoint",
}

var openAPIQueryParameters = map[string][]parameterSpec{
	"/api/testProxy":                {{Name: "url", Description: "Base64 编码的目标 URL", Required: true}},
	"/api/mikan":                    {{Name: "text", Description: "搜索关键词", Required: true}},
	"/api/mikanGroup":               {{Name: "url", Description: "Mikan 番剧页面 URL", Required: true}},
	"/api/aniBTGroup":               {{Name: "bgmId", Description: "Bangumi 番剧 ID", Required: true}},
	"/api/animeGardenList":          {{Name: "bgmUrl", Description: "可选 Bangumi URL"}},
	"/api/animeGardenGroup":         {{Name: "bgmId", Description: "Bangumi 番剧 ID", Required: true}},
	"/api/searchBgm":                {{Name: "name", Description: "搜索名称", Required: true}},
	"/api/getAniBySubjectId":        {{Name: "id", Description: "Bangumi 番剧 ID", Required: true}},
	"/api/bgm/oauth/callback":       {{Name: "code", Description: "Bangumi OAuth 授权码", Required: true}},
	"/api/deleteAni":                {{Name: "deleteFiles", Description: "是否删除媒体文件", Type: "boolean", Required: true}},
	"/api/batchEnable":              {{Name: "value", Description: "启用或禁用", Type: "boolean", Required: true}},
	"/api/updateTotalEpisodeNumber": {{Name: "force", Description: "是否强制更新", Type: "boolean", Required: true}},
	"/api/deleteTorrent":            {{Name: "id", Description: "订阅 ID", Required: true}, {Name: "hash", Description: "逗号分隔的 InfoHash", Required: true}},
	"/api/getSubtitles":             {{Name: "filename", Description: "Base64 编码的文件名", Required: true}},
	"/api/file":                     {{Name: "filename", Description: "Base64 编码的文件名", Required: true}},
	"/api/stop":                     {{Name: "status", Description: "停止或重启状态", Type: "integer", Required: true}},
	"/api/proxyImage":               {{Name: "imgUrl", Description: "Base64 编码的图片 URL", Required: true}},
}

type parameterSpec struct {
	Name        string
	Description string
	Type        string
	Required    bool
}

func (a *App) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	_ = json.NewEncoder(w).Encode(a.openapiDocument())
}

func (a *App) openapiDocument() map[string]any {
	paths := map[string]any{}
	for _, route := range a.Routes() {
		if strings.HasPrefix(route.Path, "/v3/") || strings.HasPrefix(route.Path, "/swagger") {
			continue
		}
		pathItem, _ := paths[route.Path].(map[string]any)
		if pathItem == nil {
			pathItem = map[string]any{}
		}
		operation := map[string]any{
			"operationId": operationID(route.Method, route.Path),
			"summary":     summary(route.Path),
			"tags":        []string{route.Domain},
			"responses":   standardResponses(route.Path),
		}
		if isProtectedPath(route.Path) {
			operation["security"] = []any{map[string]any{"api-key": []any{}}}
		}
		if specs := openAPIQueryParameters[route.Path]; len(specs) > 0 {
			parameters := make([]map[string]any, 0, len(specs))
			for _, spec := range specs {
				parameterType := spec.Type
				if parameterType == "" {
					parameterType = "string"
				}
				parameters = append(parameters, map[string]any{"name": spec.Name, "in": "query", "description": spec.Description, "required": spec.Required, "schema": map[string]any{"type": parameterType}})
			}
			operation["parameters"] = parameters
		}
		if body := requestBody(route.Method, route.Path); body != nil {
			operation["requestBody"] = body
		}
		pathItem[strings.ToLower(route.Method)] = operation
		paths[route.Path] = pathItem
	}
	return map[string]any{
		"openapi":      "3.0.3",
		"info":         map[string]any{"title": "ANI-RSS", "description": "基于 RSS 自动追番、订阅、下载、刮削、洗版", "version": "v" + a.version, "license": map[string]any{"name": "GPL-2.0", "url": "https://github.com/wushuo894/ani-rss/blob/master/LICENSE"}},
		"externalDocs": map[string]any{"description": "外部文档", "url": "https://docs.wushuo.top/"},
		"components":   map[string]any{"securitySchemes": map[string]any{"api-key": map[string]any{"type": "apiKey", "in": "header", "name": "api-key"}}, "schemas": map[string]any{"Result": resultSchema()}},
		"paths":        paths,
	}
}

func standardResponses(path string) map[string]any {
	if path == "/api/file" {
		return map[string]any{"200": map[string]any{"description": "媒体文件", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "403": map[string]any{"description": "禁止访问"}, "404": map[string]any{"description": "文件不存在"}}
	}
	if path == "/api/custom.js" {
		return map[string]any{"200": map[string]any{"description": "JavaScript", "content": map[string]any{"application/javascript": map[string]any{"schema": map[string]any{"type": "string"}}}}}
	}
	if path == "/api/custom.css" {
		return map[string]any{"200": map[string]any{"description": "CSS", "content": map[string]any{"text/css": map[string]any{"schema": map[string]any{"type": "string"}}}}}
	}
	if path == "/api/mcp" {
		return map[string]any{"200": map[string]any{"description": "JSON-RPC MCP 响应", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"jsonrpc"}}}, "text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}}, "403": map[string]any{"description": "缺少或无效的 API key"}}
	}
	if path == "/api/exportConfig" || path == "/api/downloadLogs" {
		return map[string]any{"200": map[string]any{"description": "ZIP 备份", "content": map[string]any{"application/zip": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "403": map[string]any{"description": "未授权"}}
	}
	if path == "/api/calendar.ics" {
		return map[string]any{"200": map[string]any{"description": "iCalendar 文件", "content": map[string]any{"text/calendar": map[string]any{"schema": map[string]any{"type": "string"}}}}, "403": map[string]any{"description": "未授权"}}
	}
	if path == "/api/proxyImage" {
		return map[string]any{"200": map[string]any{"description": "代理图片", "content": map[string]any{"image/*": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "403": map[string]any{"description": "禁止访问"}, "502": map[string]any{"description": "图片下载失败"}}
	}
	return map[string]any{"200": map[string]any{"description": "成功", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Result"}}}}, "403": map[string]any{"description": "未授权"}, "500": map[string]any{"description": "业务或外部服务失败"}}
}

func resultSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"code", "message", "t"}, "properties": map[string]any{"code": map[string]any{"type": "integer", "format": "int32"}, "message": map[string]any{"type": "string"}, "t": map[string]any{"type": "integer", "format": "int64"}, "data": map[string]any{}}}
}

func requestBody(method, path string) map[string]any {
	if method != http.MethodPost || path == "/api/ping" || path == "/api/listAni" || path == "/api/refreshAll" || path == "/api/newNotification" || path == "/api/about" || path == "/api/update" || path == "/api/meBgm" || path == "/api/bgm/oauth/callback" {
		return nil
	}
	if path == "/api/importConfig" || path == "/api/webui/upload" || path == "/api/upload" || path == "/api/uploadAndRead" || path == "/api/uploadAndReadToBase64" {
		return map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string", "format": "binary"}}, "required": []string{"file"}}}}}
	}
	bodySchema := map[string]any{"type": "object", "additionalProperties": true}
	if path == "/api/deleteAni" || path == "/api/batchEnable" || path == "/api/updateTotalEpisodeNumber" || path == "/api/batchScrape" {
		bodySchema = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	if path == "/api/importAni" {
		bodySchema = map[string]any{"type": "object", "required": []string{"aniList"}, "properties": map[string]any{"aniList": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}}
	}
	if path == "/api/mcp" {
		bodySchema = map[string]any{"type": "object", "required": []string{"jsonrpc", "method"}, "properties": map[string]any{"jsonrpc": map[string]any{"type": "string", "enum": []string{"2.0"}}, "id": map[string]any{}, "method": map[string]any{"type": "string"}, "params": map[string]any{"type": "object"}}}
	}
	return map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": bodySchema}}}
}

func isProtectedPath(path string) bool {
	switch path {
	case "/api/ping", "/api/login", "/api/testIpWhitelist", "/api/custom.js", "/api/custom.css":
		return false
	default:
		return true
	}
}

func operationID(method, path string) string {
	clean := strings.Trim(path, "/")
	clean = strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(clean)
	return strings.ToLower(method) + "_" + clean
}

func summary(path string) string {
	if value := openAPISummaries[path]; value != "" {
		return value
	}
	return "ANI-RSS " + strings.TrimPrefix(path, "/api/")
}

func (a *App) swaggerRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/swagger-ui/index.html", http.StatusFound)
}

func (a *App) swaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>ANI-RSS API</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head><body><div id="swagger-ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script><script>window.onload=()=>SwaggerUIBundle({url:'/v3/api-docs',dom_id:'#swagger-ui'});</script></body></html>`))
}
