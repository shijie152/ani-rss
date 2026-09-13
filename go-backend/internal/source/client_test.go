package source_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
)

func TestMikanSearchAndGroupParseHTMLFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/Home/Search"):
			_, _ = w.Write([]byte(`<div class="date-select"><div class="dropdown-menu"><ul><li>2026</li><li><a data-year="2026" data-season="春">春</a></li></ul></div></div><div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Demo</a></li></ul></div>`))
		case r.URL.Path == "/Home/Bangumi/123":
			_, _ = w.Write([]byte(`<div class="content"><img src="/cover.jpg"></div><div class="bangumi-title">Demo</div><div class="bangumi-info">Bangumi番组计划链接：<a href="https://bgm.tv/subject/42">BGM</a></div><div class="leftbar-item"><a class="subgroup-name" data-anchor="#group-1">Group</a><span class="date">today</span></div><section id="group-1"><a class="mikan-rss" href="/RSS/1"></a></section><table><tbody><tr><td><a>Demo [01]</a><a data-clipboard-text="magnet:?xt=1"></a><a href="/torrent/1">torrent</a></td><td>unused</td><td>1 GiB</td><td>2026-01-02</td></tr></tbody></table>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := source.New(source.Options{MikanHost: server.URL})
	result, err := client.Mikan("demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["totalItems"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	weeks := result["weeks"].([]any)
	week := weeks[0].(map[string]any)
	items := week["items"].([]any)
	item := items[0].(map[string]any)
	if item["title"] != "Demo" || item["bgmId"] != "123" {
		t.Fatalf("item = %#v", item)
	}
	groups, err := client.MikanGroup(server.URL + "/Home/Bangumi/123")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0]["label"] != "Group" || groups[0]["bgmUrl"] != "https://bgm.tv/subject/42" {
		t.Fatalf("groups = %#v", groups)
	}
	if groups[0]["updateDay"] != "today" || groups[0]["rss"] != server.URL+"/RSS/1" {
		t.Fatalf("group UI fields = %#v", groups[0])
	}
	if _, ok := groups[0]["groupRegex"].(map[string]any); !ok {
		t.Fatalf("group regex missing: %#v", groups[0])
	}
}

func TestMikanSeasonalAndAnimeGardenUIFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/Home/BangumiCoverFlowByDayOfWeek":
			_, _ = w.Write([]byte(`<div class="date-select"><span class="date-text">2026 春</span><div class="dropdown-menu"><ul><li>ignore</li><li><a data-year="2026" data-season="春">春</a></li></ul></div></div><div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Demo</a></li></ul></div>`))
		case "/subjects":
			_, _ = w.Write([]byte(`{"subjects":[{"id":"42","name":"Garden Demo","keywords":["tag"],"activedAt":"2026-01-05T00:00:00Z","isArchived":"false","score":8.3,"cover":"https://img.test/garden.jpg"}]}`))
		case "/resources":
			_, _ = w.Write([]byte(`{"resources":[{"title":"Old","size":1024,"createdAt":"2026-01-01T00:00:00Z","fetchedAt":"2026-01-03T00:00:00Z","fansub":{"id":"g","name":"Group"}},{"title":"New","size":2048,"createdAt":"2026-01-02T00:00:00Z","fansub":{"id":"g","name":"Group"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := source.New(source.Options{MikanHost: server.URL, AnimeGardenHost: server.URL})
	mikan, err := client.Mikan("", map[string]any{"year": 2026, "season": "春"})
	if err != nil || len(mikan["seasons"].([]any)) != 1 || mikan["seasons"].([]any)[0].(map[string]any)["select"] != true {
		t.Fatalf("seasonal Mikan = %#v, err=%v", mikan, err)
	}
	garden, err := client.AnimeGardenList("")
	if err != nil || len(garden) != 1 {
		t.Fatalf("garden list = %#v, err=%v", garden, err)
	}
	subject := garden[0]["subjects"].([]any)[0].(map[string]any)
	if subject["keywords"].([]any)[0] != "tag" || subject["exists"] != false || subject["weekLabel"] != "星期一" {
		t.Fatalf("garden subject fields = %#v", subject)
	}
	groups, err := client.AnimeGardenGroup("42")
	if err != nil || len(groups) != 1 || groups[0]["lastUpdatedAt"] != "2026-01-03T00:00:00Z" {
		t.Fatalf("garden group fields = %#v, err=%v", groups, err)
	}
}

func TestMikanEmptyRequestLoadsCurrentSeasonFromHomePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("Mikan empty request path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`<div class="date-select"><div class="date-text">2026 夏</div><div class="dropdown-menu"><ul><li><a data-year="2026" data-season="夏">夏</a></li></ul></div></div><div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Demo</a></li></ul></div>`))
	}))
	defer server.Close()

	result, err := source.New(source.Options{MikanHost: server.URL}).Mikan("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["totalItems"] != 1 || len(result["seasons"].([]any)) != 1 {
		t.Fatalf("home page result = %#v", result)
	}
}

func TestAniBTAnimeGardenAndBangumiClientsTransformResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/api/seasons/anime":
			body = `{"data":{"currentSeason":"2026春","byWeekday":[{"weekday":1,"weekdayLabel":"星期一","animes":[{"bgmId":"42","rating":9.1,"rssReleaseCount":1}]}]}}`
		case "/api/anime/groups":
			body = `{"data":{"groups":[{"slug":"fansub","name":"Group","items":[{"title":"Demo","size":2048}]}]}}`
		case "/subjects":
			body = `{"subjects":[{"id":"42","name":"Demo","activedAt":"2026-01-05T00:00:00Z"}]}`
		case "/resources":
			body = `{"resources":[{"title":"Demo 01","size":2048,"fansub":{"id":"g","name":"Group"}}]}`
		case "/v0/subjects/42":
			body = `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"images":{"large":"https://img.test/demo.jpg"}}`
		case "/search/subject/Demo":
			body = `{"list":[{"id":"42","name":"Demo JP","name_cn":"Demo CN"}]}`
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := source.New(source.Options{AniBTHost: server.URL, AnimeGardenHost: server.URL, BangumiAPI: server.URL, Subscriptions: func() []model.Ani { return []model.Ani{{BGMURL: "https://bgm.tv/subject/42"}} }})
	aniBT, err := client.AniBT(map[string]any{"season": "2026春"})
	if err != nil {
		t.Fatal(err)
	}
	if aniBT["currentSeason"] != "2026春" {
		t.Fatalf("anibt = %#v", aniBT)
	}
	groups, err := client.AniBTGroup("42")
	if err != nil || len(groups) != 1 || !strings.Contains(groups[0]["rss"].(string), "groupSlug=fansub") {
		t.Fatalf("anibt groups = %#v, %v", groups, err)
	}
	garden, err := client.AnimeGardenList("")
	if err != nil || len(garden) != 1 {
		t.Fatalf("garden = %#v, %v", garden, err)
	}
	gardenGroups, err := client.AnimeGardenGroup("42")
	if err != nil || len(gardenGroups) != 1 {
		t.Fatalf("garden groups = %#v, %v", gardenGroups, err)
	}
	search, err := client.SearchBangumi("Demo")
	if err != nil || len(search) != 1 {
		t.Fatalf("search = %#v, %v", search, err)
	}
	ani, err := client.SubscriptionFromSubject("42")
	if err != nil {
		t.Fatal(err)
	}
	if ani.Title != "Demo CN" || ani.TotalEpisodeNumber != 12 {
		t.Fatalf("ani = %#v", ani)
	}
}

func TestSourceClientRetriesRedirectsAndReportsNonSuccess(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "ani-rss-go" {
			t.Errorf("User-Agent = %q", got)
		}
		switch r.URL.Path {
		case "/Home/Search":
			attempts++
			if attempts < 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
		case "/final":
			_, _ = w.Write([]byte(`<div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Demo</a></li></ul></div>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := source.New(source.Options{MikanHost: server.URL, Retries: 3})
	result, err := client.Mikan("demo", nil)
	if err != nil || result["totalItems"] != 1 || attempts != 3 {
		t.Fatalf("retry result=%#v err=%v attempts=%d", result, err, attempts)
	}

	failingAttempts := 0
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failingAttempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	client = source.New(source.Options{MikanHost: failing.URL, Retries: 2})
	if _, err := client.Mikan("demo", nil); err == nil || failingAttempts != 2 || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("non-success err=%v attempts=%d", err, failingAttempts)
	}
}

func TestSourceClientFormatsSizesWithJavaBinaryUnits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"groups":[{"slug":"group","name":"Group","items":[{"title":"Demo 01","size":2048}]}]}}`))
	}))
	defer server.Close()

	client := source.New(source.Options{AniBTHost: server.URL})
	groups, err := client.AniBTGroup("42")
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %#v, err = %v", groups, err)
	}
	items := groups[0]["items"].([]any)
	if got := items[0].(map[string]any)["formatSize"]; got != "2.00 KiB" {
		t.Fatalf("formatSize = %#v", got)
	}
}

func TestSourceClientsReturnDiagnosableEmptyResultsForMissingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Home/Search":
			_, _ = w.Write([]byte(`<html><body>no results</body></html>`))
		case "/api/seasons/anime":
			_, _ = w.Write([]byte(`{"data":{"byWeekday":[{"weekday":1,"animes":[{"bgmId":"42"}]}]}}`))
		case "/resources":
			_, _ = w.Write([]byte(`{"resources":[{"title":"No group","size":1024}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := source.New(source.Options{MikanHost: server.URL, AniBTHost: server.URL, AnimeGardenHost: server.URL})
	mikan, err := client.Mikan("demo", nil)
	if err != nil || mikan["totalItems"] != 0 {
		t.Fatalf("empty Mikan = %#v, err = %v", mikan, err)
	}
	aniBT, err := client.AniBT(map[string]any{})
	if err != nil || len(aniBT["byWeekday"].([]any)) != 0 {
		t.Fatalf("missing AniBT fields = %#v, err = %v", aniBT, err)
	}
	groups, err := client.AnimeGardenGroup("42")
	if err != nil || len(groups) != 0 {
		t.Fatalf("missing AnimeGarden group = %#v, err = %v", groups, err)
	}
}
