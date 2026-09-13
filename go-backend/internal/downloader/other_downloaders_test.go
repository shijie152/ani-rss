package downloader_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

func TestTransmissionAdapterSessionAndLifecycle(t *testing.T) {
	var sessionReady atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrent" {
			_, _ = w.Write([]byte("d4:torrent"))
			return
		}
		if r.URL.Path != "/transmission/rpc" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
			t.Errorf("Transmission auth = %q", r.Header.Get("Authorization"))
		}
		if !sessionReady.Load() {
			sessionReady.Store(true)
			w.Header().Set("X-Transmission-Session-Id", "session-1")
			w.WriteHeader(http.StatusConflict)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != "session-1" {
			t.Errorf("Transmission session = %q", r.Header.Get("X-Transmission-Session-Id"))
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "torrent-get":
			_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrents":[{"id":7,"name":"Demo","labels":["ani-rss","Group"],"hashString":"ABC","isFinished":false,"status":6,"totalSize":200,"haveValid":100,"downloadDir":"/media"},{"id":8,"name":"Foreign","labels":["other"],"hashString":"OTHER","isFinished":true,"status":6,"totalSize":1,"haveValid":1,"downloadDir":"/media"}]}}`))
		case "torrent-add":
			_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrent-added":{"hashString":"NEW"}}}`))
		default:
			_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
		}
	}))
	defer server.Close()
	adapter := &downloader.Transmission{Host: server.URL, Username: "user", Password: "pass"}
	ctx := context.Background()
	if err := adapter.Login(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := adapter.Torrents(ctx)
	if err != nil || len(items) != 1 || items[0].State != "stalledUP" || items[0].Progress != 50 {
		t.Fatalf("Transmission list = %#v, err=%v", items, err)
	}
	if err := adapter.Add(ctx, model.Resource{Magnet: "magnet:?xt=urn:btih:NEW"}, "/media", []string{"ani-rss"}, false); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Add(ctx, model.Resource{TorrentURL: server.URL + "/torrent"}, "/media", nil, false); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{
		func() error { return adapter.Delete(ctx, "ABC", true) },
		func() error { return adapter.RenameFile(ctx, "ABC", "old.mkv", "new.mkv") },
		func() error { return adapter.AddTags(ctx, "ABC", "extra") },
		func() error { return adapter.SetSavePath(ctx, "ABC", "/new") },
		func() error { return adapter.Start(ctx, "ABC") },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() < 7 {
		t.Fatalf("Transmission RPC calls = %d", calls.Load())
	}
}

func TestAria2AdapterJSONRPCAndStatusMapping(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file.torrent" {
			_, _ = w.Write([]byte("d4:info"))
			return
		}
		if r.URL.Path != "/jsonrpc" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Params) == 0 || request.Params[0] != "token:secret" {
			t.Errorf("Aria2 params = %#v", request.Params)
		}
		methods = append(methods, request.Method)
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "aria2.getGlobalStat":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"ani-rss","result":{"numActive":"0"}}`))
		case "aria2.tellActive":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"ani-rss","result":[{"gid":"active","totalLength":"100","completedLength":"20","status":"active","infoHash":"HASH","dir":"/media","bittorrent":{"info":{"name":"Active"}},"files":[]}]}`))
		case "aria2.tellWaiting", "aria2.tellStopped":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"ani-rss","result":[{"gid":"done","totalLength":"100","completedLength":"100","status":"complete","infoHash":"DONE","dir":"/media","bittorrent":{"info":{"name":"Done"}},"files":[]}]}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"ani-rss","result":"ok"}`))
		}
	}))
	defer server.Close()
	adapter := &downloader.Aria2{Host: server.URL, Token: "secret"}
	ctx := context.Background()
	if err := adapter.Login(ctx); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Add(ctx, model.Resource{TorrentURL: server.URL + "/file.torrent"}, "/media", nil, false); err != nil {
		t.Fatal(err)
	}
	items, err := adapter.Torrents(ctx)
	if err != nil || len(items) != 3 {
		t.Fatalf("Aria2 list = %#v, err=%v", items, err)
	}
	if items[0].State != "downloading" || items[0].Progress != 20 || items[1].State != "stoppedUP" {
		t.Fatalf("Aria2 states = %#v", items)
	}
	if err := adapter.Delete(ctx, "done", false); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetSavePath(ctx, "done", "/new"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.UpdateTrackers(ctx, []string{"udp://tracker.test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(methods, ","), "aria2.changeGlobalOption") {
		t.Fatalf("Aria2 methods = %v", methods)
	}
}

func TestOpenListAdapterRetriesAndMapsTerminalFailures(t *testing.T) {
	var infoCalls atomic.Int32
	var retryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me":
			_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{}}`))
		case "/api/fs/add_offline_download":
			_, _ = w.Write([]byte(`{"code":200,"data":{"tasks":[{"id":"task-1"}]}}`))
		case "/api/task/offline_download/info":
			if infoCalls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"code":200,"data":{"id":"task-1","name":"Demo","state":5,"error":"temporary"}}`))
			} else {
				_, _ = w.Write([]byte(`{"code":200,"data":{"id":"task-1","name":"Demo","state":2,"progress":100,"totalBytes":"100"}}`))
			}
		case "/api/task/offline_download/retry":
			retryCalls.Add(1)
			_, _ = w.Write([]byte(`{"code":200,"data":{}}`))
		case "/api/fs/list":
			_, _ = w.Write([]byte(`{"code":200,"data":{"content":[{"name":"Demo.mkv","is_dir":false}]}}`))
		case "/api/fs/move":
			_, _ = w.Write([]byte(`{"code":200,"data":{}}`))
		case "/api/task/offline_download/undone", "/api/task/offline_download/done":
			_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter := &downloader.OpenList{Host: server.URL, Token: "openlist-secret", Provider: "115 Open", Timeout: time.Second, Retries: 2, PollInterval: time.Millisecond}
	ctx := context.Background()
	if err := adapter.Login(ctx); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Add(ctx, model.Resource{Magnet: "magnet:?xt=urn:btih:OPEN"}, "/media", nil, false); err != nil {
		t.Fatal(err)
	}
	if retryCalls.Load() != 1 || infoCalls.Load() != 2 {
		t.Fatalf("OpenList calls info=%d retry=%d", infoCalls.Load(), retryCalls.Load())
	}
	files, err := adapter.FindFiles(ctx, "/media")
	if err != nil || len(files) != 1 || files[0] != "/media/Demo.mkv" {
		t.Fatalf("OpenList files = %#v, err=%v", files, err)
	}
	if err := adapter.Move(ctx, "/media", "/library", []string{"Demo.mkv"}); err != nil {
		t.Fatal(err)
	}
	serverFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/task/offline_download/info" {
			_, _ = w.Write([]byte(`{"code":200,"data":{"id":"bad","name":"Bad","state":7,"error":"permanent"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{}}`))
	}))
	defer serverFail.Close()
	failed := &downloader.OpenList{Host: serverFail.URL, Token: "secret", Provider: "115 Open", Timeout: time.Second, Retries: 0, PollInterval: time.Millisecond}
	if _, err := failed.WaitForCompletion(ctx, "bad", time.Millisecond); err == nil {
		t.Fatal("OpenList permanent error was ignored")
	}
}

func TestDownloaderFactorySelectsAllUIValues(t *testing.T) {
	for _, typeName := range []string{"qBittorrent", "Transmission", "Aria2", "OpenList"} {
		adapter, err := downloader.New(model.Config{"downloadToolType": typeName, "downloadToolHost": "http://example.test", "downloadToolUsername": "u", "downloadToolPassword": "p", "provider": "115 Open"}, nil)
		if err != nil || adapter == nil {
			t.Fatalf("factory %s = %#v, err=%v", typeName, adapter, err)
		}
	}
}

func TestDownloaderAdaptersMapAuthenticationFailures(t *testing.T) {
	transmission := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer transmission.Close()
	if err := (&downloader.Transmission{Host: transmission.URL, Username: "u", Password: "p"}).Login(context.Background()); err == nil {
		t.Fatal("Transmission authentication failure was ignored")
	}

	aria := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"ani-rss","error":{"code":-1,"message":"invalid secret"}}`))
	}))
	defer aria.Close()
	if err := (&downloader.Aria2{Host: aria.URL, Token: "wrong"}).Login(context.Background()); err == nil {
		t.Fatal("Aria2 authentication failure was ignored")
	}

	openList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":401,"message":"invalid token"}`))
	}))
	defer openList.Close()
	if err := (&downloader.OpenList{Host: openList.URL, Token: "wrong", Provider: "115 Open"}).Login(context.Background()); err == nil {
		t.Fatal("OpenList authentication failure was ignored")
	}
}

func TestDownloaderRetriesTransientServerFailuresAndHonorsTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"ani-rss","result":{"numActive":"0"}}`))
	}))
	defer server.Close()
	if err := (&downloader.Aria2{Host: server.URL, Token: "secret"}).Login(context.Background()); err != nil {
		t.Fatalf("Aria2 transient retry = %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("Aria2 attempts = %d, want 3", attempts.Load())
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			_, _ = w.Write([]byte(`{"code":200,"data":{}}`))
		case <-context.Background().Done():
		}
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := (&downloader.OpenList{Host: slow.URL, Token: "secret", Provider: "115 Open"}).Login(ctx); err == nil {
		t.Fatal("OpenList timeout was ignored")
	}
}
