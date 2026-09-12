package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestQBittorrentAdapterUsesAuthAndMapsStatuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer qbt_test" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v4"))
		case "/api/v2/torrents/add":
			if r.ParseForm() != nil || r.Form.Get("urls") == "" || r.Form.Get("savepath") != "/media" {
				t.Errorf("form = %#v", r.Form)
			}
			_, _ = w.Write([]byte("Ok"))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte(`[{"hash":"abc","name":"Demo","state":"forcedUP","progress":0.5,"size":100,"amount_left":50,"save_path":"/media","category":"ani-rss","tags":"ani-rss,Group"}]`))
		case "/api/v2/torrents/delete":
			_, _ = w.Write([]byte("Ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter := &downloader.QBittorrent{Host: server.URL, APIKey: "qbt_test"}
	if err := adapter.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{Title: "Demo", DownloadURL: "magnet:?xt=urn:btih:abc", Magnet: "magnet:?xt=urn:btih:abc"}
	if err := adapter.Add(context.Background(), resource, "/media", []string{"ani-rss", "Group"}, true); err != nil {
		t.Fatal(err)
	}
	items, err := adapter.Torrents(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	if items[0].Progress != 50 || items[0].Downloaded != 50 || items[0].State != "forcedUP" || len(items[0].Tags) != 2 {
		t.Fatalf("mapped = %#v", items[0])
	}
	if err := adapter.Delete(context.Background(), "abc", false); err != nil {
		t.Fatal(err)
	}
}

func TestQBittorrentUsernamePasswordLoginAndFiltersForeignTasks(t *testing.T) {
	var loginSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if r.ParseForm() != nil || r.Form.Get("username") != "user" || r.Form.Get("password") != "pass" {
				t.Fatalf("login form = %#v", r.Form)
			}
			loginSeen = true
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			if r.Header.Get("Cookie") != "SID=session" {
				t.Fatalf("cookie = %q", r.Header.Get("Cookie"))
			}
			_, _ = w.Write([]byte(`[{"hash":"owned","name":"Owned","state":"stoppedUP","progress":1,"size":100,"amount_left":0,"category":"ani-rss","tags":""},{"hash":"foreign","name":"Foreign","state":"downloading","progress":0.2,"size":100,"amount_left":80,"category":"other","tags":"other"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter := &downloader.QBittorrent{Host: server.URL, Username: "user", Password: "pass"}
	if err := adapter.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !loginSeen {
		t.Fatal("login was not called")
	}
	items, err := adapter.Torrents(context.Background())
	if err != nil || len(items) != 1 || items[0].Hash != "owned" {
		t.Fatalf("filtered items = %#v, err = %v", items, err)
	}
}
