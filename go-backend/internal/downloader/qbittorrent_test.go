package downloader_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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

func TestQBittorrentAddCarriesJavaDownloadParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/add" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		expected := map[string]string{
			"addToTopOfQueue": "false", "autoTMM": "false", "category": "ani-rss",
			"contentLayout": "Subfolder", "dlLimit": "2048", "firstLastPiecePrio": "false",
			"rename": "Demo S01E01", "savepath": "/media", "sequentialDownload": "false",
			"skip_checking": "false", "stopCondition": "None", "upLimit": "1024",
			"useDownloadPath": "true", "tags": "ani-rss,Group", "ratioLimit": "1",
			"seedingTimeLimit": "3600", "inactiveSeedingTimeLimit": "7200",
			"paused": "false", "stopped": "false", "urls": "magnet:?xt=urn:btih:abc",
		}
		for key, value := range expected {
			if got := r.Form.Get(key); got != value {
				t.Errorf("form[%q] = %q, want %q", key, got, value)
			}
		}
		_, _ = w.Write([]byte("Ok"))
	}))
	defer server.Close()
	adapter := &downloader.QBittorrent{Host: server.URL, APIKey: "qbt_test", ContentLayout: "Subfolder", UseDownloadPath: true, UpLimit: 1024, DlLimit: 2048, RatioLimit: 1, SeedingTimeLimit: 3600, InactiveSeedingTimeLimit: 7200, Rename: "Demo S01E01"}
	if err := adapter.Add(context.Background(), model.Resource{Title: "Demo", Magnet: "magnet:?xt=urn:btih:abc"}, "/media", []string{"ani-rss", "Group"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestQBittorrentSetSavePathDisablesAutomaticManagementFirst(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, r.URL.Path)
		if r.URL.Path == "/api/v2/torrents/setAutoManagement" && (r.Form.Get("hashes") != "ABC" || r.Form.Get("enable") != "false") {
			t.Fatalf("auto management form = %#v", r.Form)
		}
		if r.URL.Path == "/api/v2/torrents/setSavePath" && (r.Form.Get("id") != "ABC" || r.Form.Get("path") != "/new") {
			t.Fatalf("save path form = %#v", r.Form)
		}
		_, _ = w.Write([]byte("Ok"))
	}))
	defer server.Close()
	adapter := &downloader.QBittorrent{Host: server.URL, APIKey: "qbt_test"}
	if err := adapter.SetSavePath(context.Background(), "ABC", "/new"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "/api/v2/torrents/setAutoManagement" || calls[1] != "/api/v2/torrents/setSavePath" {
		t.Fatalf("qBittorrent move calls = %#v", calls)
	}
}

func TestQBittorrentWaitsForCompletionAndReportsFailure(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			http.NotFound(w, r)
			return
		}
		state := `downloading`
		progress := 0.5
		if polls.Add(1) > 1 {
			state, progress = "stoppedUP", 1
		}
		_, _ = w.Write([]byte(`[{
			"hash":"done","name":"Demo","state":"` + state + `","progress":` + fmt.Sprintf("%.1f", progress) + `,"size":100,"amount_left":50,"category":"ani-rss","tags":"ani-rss"
		}]`))
	}))
	defer server.Close()
	adapter := &downloader.QBittorrent{Host: server.URL, APIKey: "qbt_test"}
	task, err := adapter.WaitForCompletion(context.Background(), "done", time.Millisecond)
	if err != nil || task.State != "stoppedUP" || polls.Load() < 2 {
		t.Fatalf("task=%#v err=%v polls=%d", task, err, polls.Load())
	}
	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"hash":"bad","name":"Demo","state":"error","progress":0,"size":100,"amount_left":100,"category":"ani-rss","tags":"ani-rss"}]`))
	}))
	defer failure.Close()
	failedTask, err := (&downloader.QBittorrent{Host: failure.URL, APIKey: "qbt_test"}).WaitForCompletion(context.Background(), "bad", time.Millisecond)
	if err == nil || failedTask.State != "error" {
		t.Fatalf("failed task=%#v err=%v", failedTask, err)
	}
}

// qBittorrent v5 accepts magnet adds asynchronously: it answers HTTP 202 with a
// JSON body like {"added_torrent_ids":[],"pending_count":1,...} instead of the
// legacy "Ok" body. Java's HttpResponse::isOk treats any 2xx as success, so the
// adapter must do the same or every magnet submission is reported as a failure
// and subscription progress never advances.
func TestQBittorrentAddAcceptsAcceptedMagnetResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/add" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"added_torrent_ids":[],"failure_count":0,"pending_count":1,"success_count":0}`))
	}))
	defer server.Close()
	adapter := &downloader.QBittorrent{Host: server.URL, APIKey: "qbt_test"}
	if err := adapter.Add(context.Background(), model.Resource{Title: "Demo", Magnet: "magnet:?xt=urn:btih:abc"}, "/media", []string{"ani-rss"}, false); err != nil {
		t.Fatalf("Add() on 202 pending = %v, want nil", err)
	}
}
