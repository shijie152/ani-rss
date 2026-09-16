package source

import "github.com/shijie152/ani-rss/go-backend/internal/model"

// Discovery is the stable resource-discovery boundary consumed by handlers
// and MCP. Implementations may use different upstream adapters and caching
// policies without exposing either detail to callers.
type Discovery interface {
	Mikan(string, map[string]any) (map[string]any, error)
	MikanGroup(string) ([]map[string]any, error)
	AniBT(map[string]any) (map[string]any, error)
	AniBTGroup(string) ([]map[string]any, error)
	AnimeGardenList(string) ([]map[string]any, error)
	AnimeGardenGroup(string) ([]map[string]any, error)
	SearchBangumi(string) ([]map[string]any, error)
	BangumiSubject(string) (map[string]any, error)
	SubjectEpisodeCount(string) (int, error)
	SubscriptionFromSubject(string) (model.Ani, error)
	ResolveMikanSubscription(string) (model.Ani, error)
	ResolveRSSSubscription(string, string, string, string) (model.Ani, error)
	BGMTitle(model.Ani) (string, error)
}

var _ Discovery = (*Client)(nil)
