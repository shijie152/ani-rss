package source

import "github.com/shijie152/ani-rss/go-backend/internal/model"

// Client is the stable discovery facade consumed by the backend and MCP.
// Source-specific protocol knowledge lives behind the adapters; the facade
// only selects the adapter and preserves the legacy public contract.
type Client struct {
	mikan  *MikanAdapter
	anibt  *AniBTAdapter
	garden *AnimeGardenAdapter
	bgm    *BangumiAdapter
}

// New assembles the source adapters over one shared runtime. The runtime owns
// transport, retry, cache and request lifecycle concerns so adapters cannot
// accidentally diverge on those policies.
func New(options Options) *Client {
	rt := newRuntime(options)
	mikan := newMikanAdapter(rt)
	bgm := newBangumiAdapter(rt, mikan)
	return &Client{
		mikan:  mikan,
		anibt:  newAniBTAdapter(rt),
		garden: newAnimeGardenAdapter(rt, bgm),
		bgm:    bgm,
	}
}

func (c *Client) Mikan(text string, season map[string]any) (map[string]any, error) {
	return c.mikan.Mikan(text, season)
}

func (c *Client) MikanGroup(target string) ([]map[string]any, error) {
	return c.mikan.Group(target)
}

func (c *Client) AniBT(query map[string]any) (map[string]any, error) {
	return c.anibt.List(query)
}

func (c *Client) AniBTGroup(bgmID string) ([]map[string]any, error) {
	return c.anibt.Group(bgmID)
}

func (c *Client) AnimeGardenList(bgmURL string) ([]map[string]any, error) {
	return c.garden.List(bgmURL)
}

func (c *Client) AnimeGardenGroup(bgmID string) ([]map[string]any, error) {
	return c.garden.Group(bgmID)
}

func (c *Client) SearchBangumi(name string) ([]map[string]any, error) {
	return c.bgm.Search(name)
}

func (c *Client) BangumiSubject(id string) (map[string]any, error) {
	return c.bgm.Subject(id)
}

func (c *Client) SubjectEpisodeCount(id string) (int, error) {
	return c.bgm.EpisodeCount(id)
}

func (c *Client) SubscriptionFromSubject(id string) (model.Ani, error) {
	return c.bgm.Subscription(id)
}

func (c *Client) ResolveMikanSubscription(rssURL string) (model.Ani, error) {
	return c.mikan.ResolveSubscription(rssURL)
}

func (c *Client) ResolveRSSSubscription(typeName, rssURL, bgmURL, subgroup string) (model.Ani, error) {
	switch typeName {
	case "mikan":
		return c.mikan.ResolveRSSSubscription(rssURL, bgmURL, subgroup)
	case "ani-bt":
		return c.anibt.ResolveRSSSubscription(rssURL, bgmURL, subgroup)
	case "anime-garden":
		return c.garden.ResolveRSSSubscription(rssURL, bgmURL, subgroup)
	default:
		return model.Ani{BGMURL: bgmURL, Subgroup: subgroup}, nil
	}
}

func (c *Client) BGMTitle(item model.Ani) (string, error) {
	return c.bgm.Title(item)
}

// parseMikanList remains as a package-local compatibility seam for the fuzz
// harness while the public caller now enters through MikanAdapter.
func (c *Client) parseMikanList(target string, body []byte) map[string]any {
	return c.mikan.parseList(target, body)
}
