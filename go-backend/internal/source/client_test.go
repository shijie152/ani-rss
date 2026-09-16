package source_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
)

func expectedSourceWeekOrder(today time.Weekday) []string {
	labels := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	result := make([]string, 0, len(labels))
	for day := int(today); day >= 0; day-- {
		result = append(result, labels[day])
	}
	for day := 6; day > int(today); day-- {
		result = append(result, labels[day])
	}
	return result
}

func TestMikanSearchAndGroupParseHTMLFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/Home/Search"):
			_, _ = w.Write([]byte(`<div class="date-select"><div class="dropdown-menu"><ul><li>2026</li><li><a data-year="2026" data-season="春">春</a></li></ul></div></div><div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Demo</a></li></ul></div>`))
		case r.URL.Path == "/Home/Bangumi/123":
			_, _ = w.Write([]byte(`<div class="content"><img src="/cover.jpg"></div><div class="bangumi-title">Demo</div><div class="bangumi-info">官方网站：<a href="https://demo.test">official</a></div><div class="bangumi-info">Bangumi番组计划链接：<a href="https://bgm.tv/subject/42">BGM</a></div><div class="leftbar-item"><a class="subgroup-name" data-anchor="#group-1">Group</a><span class="date">today</span></div><section id="group-1"><a class="mikan-rss" href="/RSS/1"></a></section><table><tbody><tr><td><a>Demo [01]</a><a data-clipboard-text="magnet:?xt=1"></a><a href="/torrent/1">torrent</a></td><td>unused</td><td>1 GiB</td><td>2026-01-02</td></tr></tbody></table>`))
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
	if groups[0]["bgmUrl"] != "https://bgm.tv/subject/42" {
		t.Fatalf("group Bangumi URL = %#v", groups[0])
	}
	if _, ok := groups[0]["groupRegex"].(map[string]any); !ok {
		t.Fatalf("group regex missing: %#v", groups[0])
	}
}

func TestResolveMikanSubscriptionUsesBangumiLinkInsteadOfMikanID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Home/Bangumi/123" {
			t.Fatalf("Mikan detail path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`<div class="bangumi-title">Demo</div><div class="bangumi-info">官方网站：<a href="https://demo.test">official</a></div><div class="bangumi-info">Bangumi番组计划链接：<a href="https://bgm.tv/subject/42">BGM</a></div><div class="leftbar-item"><a class="subgroup-name" data-anchor="#370">LoliHouse</a></div><section id="370"><a class="mikan-rss" href="/RSS/Bangumi?bangumiId=123&amp;subgroupid=370"></a></section>`))
	}))
	defer server.Close()

	resolved, err := source.New(source.Options{MikanHost: server.URL}).ResolveMikanSubscription(server.URL + "/RSS/Bangumi?bangumiId=123&subgroupid=370")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BGMURL != "https://bgm.tv/subject/42" || resolved.Subgroup != "LoliHouse" {
		t.Fatalf("resolved subscription = %#v", resolved)
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
	if err != nil || len(groups) != 1 || groups[0]["lastUpdatedAt"] != "2026-01-02T00:00:00Z" {
		t.Fatalf("garden group fields = %#v, err=%v", groups, err)
	}
}

func TestAnimeGardenListNormalizesNumericIDsAndUsesBangumiCoverCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/subjects":
			_, _ = w.Write([]byte(`{"subjects":[{"id":42,"name":"Garden Demo","activedAt":"2026-01-05T16:00:00.000Z"}]}`))
		case "/bgm/cover":
			_, _ = w.Write([]byte(`{"42":{"small":"https://img.test/bgm-small.jpg","large":"https://img.test/bgm-large.jpg"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := source.New(source.Options{
		AnimeGardenHost: server.URL,
		BGMCoverURL:     server.URL + "/bgm/cover",
		Subscriptions: func() []model.Ani {
			return []model.Ani{{BGMURL: "https://bgm.tv/subject/42"}}
		},
	})
	result, err := client.AnimeGardenList("")
	if err != nil || len(result) != 1 {
		t.Fatalf("AnimeGarden result = %#v, err = %v", result, err)
	}
	subject := result[0]["subjects"].([]any)[0].(map[string]any)
	if subject["id"] != float64(42) || subject["cover"] != "https://img.test/bgm-small.jpg" || subject["exists"] != true {
		t.Fatalf("normalized AnimeGarden subject = %#v", subject)
	}
	parsed, parseErr := time.Parse(time.RFC3339, "2026-01-05T16:00:00.000Z")
	if parseErr != nil || subject["weekLabel"] != []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[parsed.In(time.Local).Weekday()] {
		t.Fatalf("local-time AnimeGarden week = %#v", subject["weekLabel"])
	}
}

func TestAnimeGardenGroupNormalizesNumericFansubIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[{"id":1,"title":"Demo","size":1024,"createdAt":"2026-01-01T00:00:00Z","fansub":{"id":827,"name":"Nix-Raws"}}]}`))
	}))
	defer server.Close()

	groups, err := source.New(source.Options{AnimeGardenHost: server.URL}).AnimeGardenGroup("42")
	if err != nil || len(groups) != 1 {
		t.Fatalf("AnimeGarden numeric groups = %#v, err = %v", groups, err)
	}
	if groups[0]["id"] != "827" || groups[0]["name"] != "Nix-Raws" || len(groups[0]["items"].([]any)) != 1 {
		t.Fatalf("normalized AnimeGarden group = %#v", groups[0])
	}
}

func TestMikanEmptyRequestLoadsCurrentSeasonFromHomePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("Mikan empty request path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`<div class="date-select"><div class="date-text">2026 夏</div><div class="dropdown-menu"><ul><li><a data-year="2026" data-season="夏">夏</a></li></ul></div></div><div class="sk-bangumi">
  <h3>星期一</h3>
  <ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Demo</a></li></ul>
</div>`))
	}))
	defer server.Close()

	result, err := source.New(source.Options{MikanHost: server.URL}).Mikan("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["totalItems"] != 1 || len(result["seasons"].([]any)) != 1 {
		t.Fatalf("home page result = %#v", result)
	}
	weeks := result["weeks"].([]any)
	if len(weeks) != 1 || weeks[0].(map[string]any)["weekLabel"] != "星期一" {
		t.Fatalf("home page week label = %#v", weeks)
	}
}

func TestMikanSearchDoesNotTreatMalformedIDAsDetailAndIgnoresDropdownItems(t *testing.T) {
	var searchQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Home/Search" {
			t.Fatalf("malformed Mikan id used detail path = %q", r.URL.Path)
		}
		searchQuery = r.URL.Query()
		_, _ = w.Write([]byte(`<div class="date-select"><div class="dropdown-menu"><ul><li><a href="/calendar" data-year="2026" data-season="春">春</a></li></ul></div></div><ul class="an-ul"></ul>`))
	}))
	defer server.Close()

	result, err := source.New(source.Options{MikanHost: server.URL}).Mikan("id: 123x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if searchQuery.Get("searchstr") != "id: 123x" {
		t.Fatalf("malformed id search query = %v", searchQuery)
	}
	if result["totalItems"] != 0 {
		t.Fatalf("dropdown item was parsed as an anime = %#v", result)
	}
	weeks := result["weeks"].([]any)
	if len(weeks) != 1 || len(weeks[0].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("empty search result = %#v", weeks)
	}
}

func TestMikanSearchIgnoresNavigationItemsInsideTheResultList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Home/Search" {
			t.Fatalf("Mikan search path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`<ul class="an-ul"><li><a href="/">主页</a></li><li><a href="/Home/MyBangumi">订阅</a></li><li><a href="/Home/Classic">列表</a></li><li><span data-src="/cover.jpg"></span><a class="an-text" href="/Home/Bangumi/123">Demo</a></li></ul>`))
	}))
	defer server.Close()

	result, err := source.New(source.Options{MikanHost: server.URL}).Mikan("demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	weeks := result["weeks"].([]any)
	if len(weeks) != 1 {
		t.Fatalf("navigation items created extra weeks = %#v", weeks)
	}
	items := weeks[0].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Demo" {
		t.Fatalf("navigation items leaked into Mikan results = %#v", items)
	}
}

func TestAniBTAndAnimeGardenUseJavaWeekOrderAndGardenRSSEncoding(t *testing.T) {
	labels := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	dates := []string{"2026-01-04", "2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-10"}
	var gardenResources []string
	var gardenSubjects []string
	for day, label := range labels {
		gardenResources = append(gardenResources, `{"id":"`+strconv.Itoa(day)+`","title":"`+label+`","size":1,"createdAt":"2026-01-01T00:00:00Z","fansub":{"id":"group-`+strconv.Itoa(day)+`","name":"A & B `+label+`"}}`)
		gardenSubjects = append(gardenSubjects, `{"id":"`+strconv.Itoa(day)+`","name":"`+label+`","activedAt":"`+dates[day]+`T00:00:00Z"}`)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/seasons/anime":
			var weekdays []string
			for day, label := range labels {
				weekdays = append(weekdays, `{"weekday":`+strconv.Itoa(day+1)+`,"weekdayLabel":"`+label+`","animes":[{"bgmId":"`+strconv.Itoa(day)+`","rating":1,"rssReleaseCount":1}]}`)
			}
			_, _ = w.Write([]byte(`{"data":{"byWeekday":[` + strings.Join(weekdays, ",") + `]}}`))
		case "/subjects":
			_, _ = w.Write([]byte(`{"subjects":[` + strings.Join(gardenSubjects, ",") + `]}`))
		case "/resources":
			_, _ = w.Write([]byte(`{"resources":[` + strings.Join(gardenResources, ",") + `]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := source.New(source.Options{AniBTHost: server.URL, AnimeGardenHost: server.URL})
	aniBT, err := client.AniBT(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	aniWeeks := aniBT["byWeekday"].([]any)
	expected := expectedSourceWeekOrder(time.Now().Weekday())
	if len(aniWeeks) != len(expected) {
		t.Fatalf("AniBT weeks = %#v", aniWeeks)
	}
	for index, label := range expected {
		if aniWeeks[index].(map[string]any)["weekdayLabel"] != label {
			t.Fatalf("AniBT week order = %#v, expected %v", aniWeeks, expected)
		}
	}

	garden, err := client.AnimeGardenList("")
	if err != nil {
		t.Fatal(err)
	}
	if len(garden) != len(expected) {
		t.Fatalf("AnimeGarden weeks = %#v", garden)
	}
	for index, label := range expected {
		if garden[index]["weekLabel"] != label {
			t.Fatalf("AnimeGarden week order = %#v, expected %v", garden, expected)
		}
	}

	groups, err := client.AnimeGardenGroup("42")
	if err != nil || len(groups) == 0 {
		t.Fatalf("AnimeGarden groups = %#v, err=%v", groups, err)
	}
	var rss string
	for _, group := range groups {
		if group["name"] == "A & B 星期日" {
			rss, _ = group["rss"].(string)
			break
		}
	}
	if want := server.URL + "/feed.xml?subject=42&fansub=A %26 B 星期日"; rss != want {
		t.Fatalf("AnimeGarden RSS = %q, want %q", rss, want)
	}
}

func TestMikanUsesMikanIDNamespaceAndSkipsEmptyWeeks(t *testing.T) {
	client := source.New(source.Options{
		MikanHost: "https://mikan.test",
		Subscriptions: func() []model.Ani {
			return []model.Ani{
				{ID: "mikan", URL: "https://mikan.test/RSS/Bangumi?bangumiId=123&subgroupid=1", BGMURL: "https://bgm.tv/subject/999"},
			}
		},
	})
	// Exercise the HTML parser through a local server so the subscription's
	// Mikan id is checked independently from its Bangumi subject id.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<div class="date-select"><span class="date-text">2026 春（更新）</span><div class="dropdown-menu"><a data-year="2026" data-season="春">春</a></div></div><div class="sk-bangumi"><h3>星期日</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Mikan Demo</a></li></ul></div><div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"></ul></div>`))
	}))
	defer server.Close()
	client = source.New(source.Options{MikanHost: server.URL, Subscriptions: func() []model.Ani {
		return []model.Ani{{URL: server.URL + "/RSS/Bangumi?bangumiId=123&subgroupid=1", BGMURL: "https://bgm.tv/subject/999"}}
	}})
	result, err := client.Mikan("demo", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	seasons := result["seasons"].([]any)
	if len(seasons) != 1 || seasons[0].(map[string]any)["select"] != true {
		t.Fatalf("season prefix matching = %#v", seasons)
	}
	weeks := result["weeks"].([]any)
	if len(weeks) != 1 || weeks[0].(map[string]any)["weekLabel"] != "星期日" {
		t.Fatalf("empty Mikan week was returned = %#v", weeks)
	}
	item := weeks[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	if item["exists"] != true {
		t.Fatalf("Mikan existence used Bangumi namespace = %#v", item)
	}
}

func TestBGMTitleResolvesMikanRSSFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/Home/Bangumi/123":
			_, _ = w.Write([]byte(`<div class="bangumi-title">Mikan Demo</div><div class="bangumi-info">Bangumi番组计划链接：<a href="https://bgm.tv/subject/42">BGM</a></div><div class="leftbar-item"><a class="subgroup-name" data-anchor="#1">Group</a></div><section id="1"><a class="mikan-rss" href="/RSS/1"></a></section>`))
		case "/v0/subjects/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"Demo JP","name_cn":"Demo CN","date":"2024-01-02"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := source.New(source.Options{MikanHost: server.URL, BangumiAPI: server.URL, Config: model.Config{}})
	title, err := client.BGMTitle(model.Ani{Type: "mikan", URL: server.URL + "/RSS/Bangumi?bangumiId=123&subgroupid=1"})
	if err != nil || title != "Demo CN" {
		t.Fatalf("Mikan BGM fallback title=%q err=%v", title, err)
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
			body = `{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2026-01-02","season":2,"platform":"TV","rating":{"score":8.5},"images":{"large":"https://img.test/demo.jpg"}}`
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
	if ani.Title != "Demo CN" || ani.TotalEpisodeNumber != 12 || ani.ReleaseDate != "2026-01-02" || ani.Season != 2 || ani.Offset != 0 || ani.Score != 8.5 {
		t.Fatalf("ani = %#v", ani)
	}
}

func TestSubscriptionFromSubjectMatchesBangumiMetadataDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/subjects/42":
			_, _ = w.Write([]byte(`{"id":42,"name":"Demo JP","name_cn":"Demo CN","eps":12,"date":"2024-01-02","platform":"TV","tags":[{"name":"Season 2"}],"rating":{"score":8.5},"images":{"small":"small.jpg","medium":"medium.jpg","large":"large.jpg"}}`))
		case "/v0/episodes":
			if r.URL.Query().Get("subject_id") != "42" || r.URL.Query().Get("type") != "0" || r.URL.Query().Get("limit") != "1000" || r.URL.Query().Get("offset") != "0" {
				t.Errorf("episodes query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0},{"type":0}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := source.New(source.Options{
		BangumiAPI: server.URL,
		Config:     model.Config{"bgmImageSize": "small", "bgmJpName": true},
	})
	item, err := client.SubscriptionFromSubject("42")
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "Demo JP" || item.Image != "small.jpg" || item.Season != 2 || item.TotalEpisodeNumber != 14 || item.Score != 8.5 {
		t.Fatalf("subject defaults = %#v", item)
	}
	if item.URL != "" || item.Type != "" || item.CustomDownloadPath || item.GlobalExclude || item.DownloadNew {
		t.Fatalf("subject conversion leaked RSS/direct-handler defaults = %#v", item)
	}
}

func TestBGMTitleHonorsTMDBAndBangumiTitleDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"name":"Demo JP","name_cn":"Demo CN","date":"2024-01-02"}`))
	}))
	defer server.Close()

	client := source.New(source.Options{
		BangumiAPI: server.URL,
		Config: model.Config{
			"bgmJpName":      false,
			"titleYear":      true,
			"tmdbId":         true,
			"tmdbIdPlexMode": true,
		},
	})
	title, err := client.BGMTitle(model.Ani{
		BGMURL: "https://bgm.tv/subject/42",
		TMDB:   map[string]any{"id": "99", "first_air_date": "2024-01-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if title != "Demo CN (2024) {tmdb-99}" {
		t.Fatalf("final BGM title = %q", title)
	}

	withoutTMDBID, err := client.BGMTitle(model.Ani{BGMURL: "https://bgm.tv/subject/42"})
	if err != nil {
		t.Fatal(err)
	}
	if withoutTMDBID != "Demo CN (2024)" {
		t.Fatalf("title without TMDB id = %q", withoutTMDBID)
	}
}

func TestSearchBangumiReturnsEmptySuccessForLegacyEmptyCases(t *testing.T) {
	called := false
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatalf("blank search made an HTTP request: %s", r.URL.String())
	}))
	client := source.New(source.Options{BangumiAPI: empty.URL})
	result, err := client.SearchBangumi("   ")
	empty.Close()
	if err != nil || result == nil || len(result) != 0 || called {
		t.Fatalf("blank search result=%#v err=%v called=%v", result, err, called)
	}

	cases := []struct {
		name string
		code int
		body string
	}{
		{name: "not found", code: http.StatusNotFound},
		{name: "server error", code: http.StatusBadGateway},
		{name: "invalid json", code: http.StatusOK, body: "not-json"},
		{name: "api 404", code: http.StatusOK, body: `{"code":404}`},
		{name: "missing list", code: http.StatusOK, body: `{}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(testCase.code)
				if testCase.body != "" {
					_, _ = w.Write([]byte(testCase.body))
				}
			}))
			defer server.Close()

			result, err := source.New(source.Options{BangumiAPI: server.URL}).SearchBangumi("Demo")
			if err != nil || result == nil || len(result) != 0 {
				t.Fatalf("search result=%#v err=%v", result, err)
			}
		})
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

func TestSourceCatalogCacheReusesUpstreamAndRefreshesSubscriptionMarkers(t *testing.T) {
	var mikanCalls atomic.Int32
	var aniBTCalls atomic.Int32
	var subscribed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Home/Search":
			mikanCalls.Add(1)
			_, _ = w.Write([]byte(`<div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/123">Mikan Demo</a></li></ul></div>`))
		case "/api/seasons/anime":
			aniBTCalls.Add(1)
			_, _ = w.Write([]byte(`{"data":{"byWeekday":[{"weekday":1,"weekdayLabel":"星期一","animes":[{"bgmId":"42","rating":8,"rssReleaseCount":1}]}]}}`))
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
	}))
	defer server.Close()

	cache := source.NewCache(16)
	client := source.New(source.Options{
		MikanHost: server.URL,
		AniBTHost: server.URL,
		Cache:     cache,
		Subscriptions: func() []model.Ani {
			if !subscribed.Load() {
				return nil
			}
			return []model.Ani{
				{URL: server.URL + "/RSS/Bangumi?bangumiId=123"},
				{BGMURL: "https://bgm.tv/subject/42"},
			}
		},
	})

	firstMikan, err := client.Mikan("demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstMikanItem := firstMikan["weeks"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	if firstMikanItem["exists"] != false {
		t.Fatalf("initial Mikan marker = %#v", firstMikanItem["exists"])
	}
	firstAniBT, err := client.AniBT(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	firstAniBTItem := firstAniBT["byWeekday"].([]any)[0].(map[string]any)["animes"].([]any)[0].(map[string]any)
	if firstAniBTItem["exists"] != false {
		t.Fatalf("initial AniBT marker = %#v", firstAniBTItem["exists"])
	}

	subscribed.Store(true)
	secondMikan, err := client.Mikan("demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	secondMikanItem := secondMikan["weeks"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	secondAniBT, err := client.AniBT(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	secondAniBTItem := secondAniBT["byWeekday"].([]any)[0].(map[string]any)["animes"].([]any)[0].(map[string]any)
	if secondMikanItem["exists"] != true || secondAniBTItem["exists"] != true {
		t.Fatalf("cached markers = Mikan %#v, AniBT %#v", secondMikanItem["exists"], secondAniBTItem["exists"])
	}
	if mikanCalls.Load() != 1 || aniBTCalls.Load() != 1 {
		t.Fatalf("upstream calls = Mikan %d, AniBT %d", mikanCalls.Load(), aniBTCalls.Load())
	}
}
