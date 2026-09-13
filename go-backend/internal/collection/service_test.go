package collection_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/collection"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestPreviewFiltersRenamesAndDetectsSubgroup(t *testing.T) {
	data := collectionTorrent(t)
	config := &fakeConfig{value: model.Config{"renameTemplate": "[${subgroup}] ${title} S${seasonFormat}E${episodeFormat}"}}
	service := &collection.Service{Config: config}
	info := model.CollectionInfo{Torrent: base64.StdEncoding.EncodeToString(data), Ani: model.Ani{
		Title: "Demo", Season: 1, Subgroup: "Group", Offset: 1,
		Match: []string{`\.(mkv|ass)$`}, Exclude: []string{"Extras"},
	}}
	items, err := service.Preview(info)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Episode != 2 || items[1].Episode != 2 {
		t.Fatalf("preview = %#v", items)
	}
	if items[0].ReName != "[Group] Demo S01E02.mkv" || items[1].ReName != "[Group] Demo S01E02.chs.ass" {
		t.Fatalf("renames = %#v", items)
	}
	group, err := service.Subgroup(info)
	if err != nil || group != "Group" {
		t.Fatalf("subgroup=%q err=%v", group, err)
	}
}

func TestStartCollectionUsesQBTorrentFileReconciliation(t *testing.T) {
	data := collectionTorrent(t)
	metaInfoHash := "" // filled from the request-independent test metainfo below
	// The fake returns its files for any hash, which also verifies that the
	// collection service uses the metainfo info-hash instead of the display name.
	var mu sync.Mutex
	var paths []string
	var priorities []string
	var started bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v4")
		case "/api/v2/auth/login":
			_, _ = io.WriteString(w, "Ok")
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			if r.FormValue("paused") != "true" || r.FormValue("rename") == "" {
				t.Errorf("add fields = %#v", r.MultipartForm.Value)
			}
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/files":
			_, _ = io.WriteString(w, `[{"index":0,"name":"Demo Collection/[Group] Demo E01.mkv","size":100,"priority":1},{"index":1,"name":"Demo Collection/[Group] Demo E01.chs.ass","size":20,"priority":1},{"index":2,"name":"Demo Collection/Extras.txt","size":5,"priority":1}]`)
		case "/api/v2/torrents/renameFile":
			_ = r.ParseForm()
			mu.Lock()
			paths = append(paths, r.FormValue("oldPath")+"=>"+r.FormValue("newPath"))
			mu.Unlock()
		case "/api/v2/torrents/filePrio":
			_ = r.ParseForm()
			mu.Lock()
			priorities = append(priorities, r.FormValue("id")+":"+r.FormValue("priority"))
			mu.Unlock()
		case "/api/v2/torrents/start":
			started = true
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	config := &fakeConfig{value: model.Config{"renameTemplate": "[${subgroup}] ${title} S${seasonFormat}E${episodeFormat}", "downloadToolHost": server.URL}}
	qb := &downloader.QBittorrent{Host: server.URL, APIKey: "test", Client: server.Client()}
	service := &collection.Service{Config: config, Download: qb, Sleep: func(time.Duration) {}}
	info := model.CollectionInfo{Torrent: base64.StdEncoding.EncodeToString(data), Ani: model.Ani{Title: "Demo", Season: 1, Subgroup: "Group", Offset: 1, CustomDownloadPathTemplate: "/media/Demo"}}
	if err := service.Start(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || !strings.Contains(strings.Join(paths, "|"), "S01E02.mkv") || !strings.Contains(strings.Join(paths, "|"), "S01E02.chs.ass") {
		t.Fatalf("renames=%v hash=%s", paths, metaInfoHash)
	}
	if len(priorities) != 1 || priorities[0] != "2:0" || !started {
		t.Fatalf("priorities=%v started=%v", priorities, started)
	}
}

type fakeConfig struct{ value model.Config }

func (f *fakeConfig) Snapshot() model.Config { return f.value }

func collectionTorrent(t *testing.T) []byte {
	t.Helper()
	info := bdict(map[string][]byte{
		"files": blist(
			bdict(map[string][]byte{"length": bint(100), "path": blist(bstr("[Group] Demo E01.mkv"))}),
			bdict(map[string][]byte{"length": bint(20), "path": blist(bstr("[Group] Demo E01.chs.ass"))}),
			bdict(map[string][]byte{"length": bint(5), "path": blist(bstr("Extras.txt"))}),
		),
		"name": bstr("Demo Collection"),
	})
	return bdict(map[string][]byte{"info": info})
}

func bstr(value string) []byte { return []byte(strconv.Itoa(len(value)) + ":" + value) }
func bint(value int64) []byte  { return []byte("i" + strconv.FormatInt(value, 10) + "e") }
func blist(values ...[]byte) []byte {
	result := []byte("l")
	for _, value := range values {
		result = append(result, value...)
	}
	return append(result, 'e')
}
func bdict(values map[string][]byte) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []byte("d")
	for _, key := range keys {
		result = append(result, bstr(key)...)
		result = append(result, values[key]...)
	}
	return append(result, 'e')
}
