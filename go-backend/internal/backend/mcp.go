package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shijie152/ani-rss/go-backend/internal/mcp"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
)

type mcpRSSInput struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	BGMURL   string `json:"bgmUrl"`
	Subgroup string `json:"subgroup"`
	Enable   *bool  `json:"enable"`
}

func (a *App) mcpTools() []mcp.Tool {
	return []mcp.Tool{
		{Name: "list_subscriptions", Description: "列出现有 ANI-RSS 订阅，可按启用状态过滤", InputSchema: mcp.ObjectSchema(map[string]any{
			"enabled": map[string]any{"type": "boolean", "description": "可选的启用状态过滤"},
		}), Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
			var input struct {
				Enabled *bool `json:"enabled"`
			}
			if err := decodeMCP(arguments, &input); err != nil {
				return nil, err
			}
			items := a.subscriptions.Items()
			filtered := make([]model.Ani, 0, len(items))
			for _, item := range items {
				if input.Enabled == nil || *input.Enabled == item.Enable {
					filtered = append(filtered, item)
				}
			}
			return filtered, nil
		}},
		{Name: "search_mikan", Description: "按关键词搜索 Mikan 番剧，可传入季度条件", InputSchema: mcp.ObjectSchema(map[string]any{
			"text":   map[string]any{"type": "string", "description": "搜索关键词，默认空字符串"},
			"season": map[string]any{"type": "object", "description": "可选季度过滤", "properties": map[string]any{"year": map[string]any{"type": "integer"}, "season": map[string]any{"type": "string"}}},
		}), Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
			var input struct {
				Text   string         `json:"text"`
				Season map[string]any `json:"season"`
			}
			if err := decodeMCP(arguments, &input); err != nil {
				return nil, err
			}
			if input.Season == nil {
				input.Season = map[string]any{}
			}
			client, err := a.sourceClient()
			if err != nil {
				return nil, err
			}
			return client.Mikan(input.Text, input.Season)
		}},
		{Name: "search_anibt", Description: "搜索 AniBT 番剧", InputSchema: mcp.ObjectSchema(nil), Call: func(_ context.Context, _ json.RawMessage) (any, error) {
			client, err := a.sourceClient()
			if err != nil {
				return nil, err
			}
			return client.AniBT(map[string]any{})
		}},
		{Name: "search_anime_garden", Description: "搜索 AnimeGarden 番剧列表", InputSchema: mcp.ObjectSchema(nil), Call: func(_ context.Context, _ json.RawMessage) (any, error) {
			client, err := a.sourceClient()
			if err != nil {
				return nil, err
			}
			return client.AnimeGardenList("")
		}},
		{Name: "get_mikan_groups", Description: "根据 Mikan 番剧页面 URL 获取字幕组 RSS", InputSchema: mcp.ObjectSchema(map[string]any{
			"url": map[string]any{"type": "string", "description": "蜜柑番剧页面 URL"},
		}), Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
			value, err := mcp.StringArg(arguments, "url", true)
			if err != nil {
				return nil, err
			}
			client, err := a.sourceClient()
			if err != nil {
				return nil, err
			}
			return client.MikanGroup(value)
		}},
		{Name: "get_anibt_groups", Description: "根据 AniBT BGM ID 获取字幕组 RSS", InputSchema: mcp.ObjectSchema(map[string]any{
			"bgmId": map[string]any{"type": "string", "description": "BGM 番剧 ID"},
		}), Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
			value, err := mcp.StringArg(arguments, "bgmId", true)
			if err != nil {
				return nil, err
			}
			client, err := a.sourceClient()
			if err != nil {
				return nil, err
			}
			return client.AniBTGroup(value)
		}},
		{Name: "get_anime_garden_groups", Description: "根据 AnimeGarden BGM ID 获取字幕组 RSS", InputSchema: mcp.ObjectSchema(map[string]any{
			"bgmId": map[string]any{"type": "string", "description": "BGM 番剧 ID"},
		}), Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
			value, err := mcp.StringArg(arguments, "bgmId", true)
			if err != nil {
				return nil, err
			}
			client, err := a.sourceClient()
			if err != nil {
				return nil, err
			}
			return client.AnimeGardenGroup(value)
		}},
		{Name: "preview_subscription_items", Description: "预览某个 RSS 订阅最终会命中的原始剧集条目", InputSchema: mcp.ObjectSchema(map[string]any{
			"url":      map[string]any{"type": "string", "description": "RSS URL"},
			"type":     map[string]any{"type": "string", "description": "mikan/ani-bt/anime-garden/other"},
			"bgmUrl":   map[string]any{"type": "string", "description": "BGM 地址"},
			"subgroup": map[string]any{"type": "string", "description": "字幕组名"},
			"enable":   map[string]any{"type": "boolean", "description": "是否启用"},
		}), Call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			item, err := a.mcpSubscription(arguments)
			if err != nil {
				return nil, fmt.Errorf("RSS解析失败: %w", err)
			}
			coordinator, err := a.newCoordinator()
			if err != nil {
				return nil, err
			}
			resources, err := coordinator.Preview(ctx, item)
			if err != nil {
				return nil, err
			}
			return map[string]any{"subscription": item, "items": resources}, nil
		}},
		{Name: "add_subscription", Description: "添加一个 ANI-RSS 订阅。需要预览命中条目时请先调用 preview_subscription_items", InputSchema: mcp.ObjectSchema(map[string]any{
			"url":      map[string]any{"type": "string", "description": "RSS URL"},
			"type":     map[string]any{"type": "string", "description": "mikan/ani-bt/anime-garden/other"},
			"bgmUrl":   map[string]any{"type": "string", "description": "BGM 地址"},
			"subgroup": map[string]any{"type": "string", "description": "字幕组名"},
			"enable":   map[string]any{"type": "boolean", "description": "是否启用"},
		}), Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
			item, err := a.mcpSubscription(arguments)
			if err != nil {
				return nil, fmt.Errorf("创建订阅失败: %w", err)
			}
			if err := a.subscriptions.Add(item); err != nil {
				return nil, err
			}
			return item, nil
		}},
	}
}

func (a *App) mcpSubscription(arguments json.RawMessage) (model.Ani, error) {
	var input mcpRSSInput
	if err := decodeMCP(arguments, &input); err != nil {
		return model.Ani{}, err
	}
	if strings.TrimSpace(input.URL) == "" {
		return model.Ani{}, errors.New("RSS URL 不能为空")
	}
	id := source.SubjectID(input.BGMURL)
	if id == "" {
		id = source.SubjectID(input.URL)
	}
	client, err := a.sourceClient()
	if err != nil {
		return model.Ani{}, err
	}
	item, err := client.SubscriptionFromSubject(id)
	if err != nil {
		return model.Ani{}, err
	}
	item.URL = input.URL
	item.Type = defaultString(input.Type, "mikan")
	item.Subgroup = input.Subgroup
	if input.Enable == nil {
		item.Enable = true
	} else {
		item.Enable = *input.Enable
	}
	return item, nil
}

func decodeMCP(data json.RawMessage, target any) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
