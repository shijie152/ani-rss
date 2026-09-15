package model

import (
	"encoding/json"
	"testing"
)

func FuzzAniJSON(f *testing.F) {
	f.Add([]byte(`{"id":"demo","title":"Demo","url":"https://example.test/rss","match":"[\"1080p\"]"}`))
	f.Add([]byte(`{"id":"demo","title":"Demo","url":"https://example.test/rss","match":[]}`))
	f.Add([]byte(`{"id":"demo","title":"Demo","url":"https://example.test/rss","tmdb":{"id":42}}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		var item Ani
		_ = json.Unmarshal(body, &item)
	})
}
