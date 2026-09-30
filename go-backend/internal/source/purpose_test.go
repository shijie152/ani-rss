package source_test

import (
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
)

// 每个用途接口只暴露该用途需要的方法：只查 Bangumi 元数据的调用方不该
// 依赖 Mikan/AniBT/AnimeGarden 的目录发现方法。
type bangumiOnly struct{}

func (bangumiOnly) SearchBangumi(string) ([]map[string]any, error)    { return nil, nil }
func (bangumiOnly) BangumiSubject(string) (map[string]any, error)     { return nil, nil }
func (bangumiOnly) SubjectEpisodeCount(string) (int, error)           { return 0, nil }
func (bangumiOnly) SubscriptionFromSubject(string) (model.Ani, error) { return model.Ani{}, nil }
func (bangumiOnly) BGMTitle(model.Ani) (string, error)                { return "", nil }

type catalogueOnly struct{}

func (catalogueOnly) Mikan(string, map[string]any) (map[string]any, error) { return nil, nil }
func (catalogueOnly) MikanGroup(string) ([]map[string]any, error)          { return nil, nil }
func (catalogueOnly) AniBT(map[string]any) (map[string]any, error)         { return nil, nil }
func (catalogueOnly) AniBTGroup(string) ([]map[string]any, error)          { return nil, nil }
func (catalogueOnly) AnimeGardenList(string) ([]map[string]any, error)     { return nil, nil }
func (catalogueOnly) AnimeGardenGroup(string) ([]map[string]any, error)    { return nil, nil }

type rssOnly struct{}

func (rssOnly) ResolveMikanSubscription(string) (model.Ani, error) { return model.Ani{}, nil }
func (rssOnly) ResolveRSSSubscription(string, string, string, string) (model.Ani, error) {
	return model.Ani{}, nil
}

func TestPurposeInterfacesAcceptNarrowImplementations(t *testing.T) {
	var bangumi source.BangumiMetadata = bangumiOnly{}
	var catalogue source.CatalogueDiscovery = catalogueOnly{}
	var rss source.RSSResolver = rssOnly{}

	if bangumi == nil || catalogue == nil || rss == nil {
		t.Fatal("narrow implementations must satisfy their purpose interface")
	}
}

// 宽接口保留（expand 阶段）：未迁移的调用方仍可用，且 Client 同时满足全部。
func TestClientStillSatisfiesEveryPurposeInterface(t *testing.T) {
	var client any = source.New(source.Options{})
	if _, ok := client.(source.BangumiMetadata); !ok {
		t.Fatal("Client must satisfy BangumiMetadata")
	}
	if _, ok := client.(source.CatalogueDiscovery); !ok {
		t.Fatal("Client must satisfy CatalogueDiscovery")
	}
	if _, ok := client.(source.RSSResolver); !ok {
		t.Fatal("Client must satisfy RSSResolver")
	}
}
