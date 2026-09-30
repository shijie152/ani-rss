package source

import "github.com/shijie152/ani-rss/go-backend/internal/model"

// BangumiMetadata is the metadata purpose: titles, subject details, episode
// counts and the subscription draft derived from a subject. Callers that only
// resolve metadata depend on these five methods, not on catalogue discovery.
type BangumiMetadata interface {
	SearchBangumi(string) ([]map[string]any, error)
	BangumiSubject(string) (map[string]any, error)
	SubjectEpisodeCount(string) (int, error)
	SubscriptionFromSubject(string) (model.Ani, error)
	BGMTitle(model.Ani) (string, error)
}

// CatalogueDiscovery is the catalogue purpose: the per-source season/week
// listings and their subgroup details.
type CatalogueDiscovery interface {
	Mikan(string, map[string]any) (map[string]any, error)
	MikanGroup(string) ([]map[string]any, error)
	AniBT(map[string]any) (map[string]any, error)
	AniBTGroup(string) ([]map[string]any, error)
	AnimeGardenList(string) ([]map[string]any, error)
	AnimeGardenGroup(string) ([]map[string]any, error)
}

// RSSResolver is the subscription purpose: turning a resource feed into a
// subscription draft.
type RSSResolver interface {
	ResolveMikanSubscription(string) (model.Ani, error)
	ResolveRSSSubscription(string, string, string, string) (model.Ani, error)
}

var _ BangumiMetadata = (*Client)(nil)
var _ CatalogueDiscovery = (*Client)(nil)
var _ RSSResolver = (*Client)(nil)
