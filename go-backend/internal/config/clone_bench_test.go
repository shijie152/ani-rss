package config

import (
	"encoding/json"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func benchConfig() model.Config {
	return model.Config{
		"downloadToolType": "qBittorrent", "downloadToolHost": "http://localhost:8080",
		"scrape": true, "autoDisabled": true, "downloadCount": 3,
		"exclude": []any{"720p", "x264", "hevc"},
		"notificationConfigList": []any{
			map[string]any{"enable": true, "notificationType": "WEB_HOOK", "statusList": []any{"DOWNLOAD_START", "DOWNLOAD_END", "COMPLETED"}, "webHookUrl": "http://127.0.0.1/n"},
			map[string]any{"enable": false, "notificationType": "TELEGRAM", "retry": 3},
		},
		"proxy": true, "proxyHost": "127.0.0.1", "proxyPort": 7890,
		"rssTimeout": 20, "renameSleepSeconds": 10,
	}
}

func jsonClone(value any) model.Config {
	data, _ := json.Marshal(value)
	var result model.Config
	_ = json.Unmarshal(data, &result)
	return result
}

func BenchmarkCloneJSON(b *testing.B) {
	cfg := benchConfig()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = jsonClone(cfg)
	}
}

func BenchmarkCloneMap(b *testing.B) {
	cfg := benchConfig()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = clone(cfg)
	}
}
