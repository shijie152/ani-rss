package notification_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/notification"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

func configReader(t *testing.T, list []any) *appconfig.Manager {
	t.Helper()
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Update(model.Config{"notificationConfigList": list}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDispatcherFiltersRendersRetriesAndDeduplicatesWebhook(t *testing.T) {
	var requests atomic.Int32
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("header = %q", r.Header.Get("X-Test"))
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := configReader(t, []any{map[string]any{
		"enable": true, "retry": 2, "sort": 1, "notificationType": "WEB_HOOK", "statusList": []any{"DOWNLOAD_END"},
		"notificationTemplate": "${emoji}${title} ${episodeFormat} ${action} ${comment}", "comment": "hello", "webHookMethod": "POST", "webHookUrl": server.URL, "webHookHeader": "X-Test: yes", "webHookBody": "${notification}",
	}})
	d := notification.New(m, t.TempDir(), nil, nil)
	event := notification.Event{Ani: model.Ani{ID: "ani-1", Title: "Demo", Season: 2, Message: true}, Resource: &model.Resource{InfoHash: "hash", Episode: 3}, Status: notification.DownloadEnd, Text: "完成"}
	if err := d.Dispatch(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := d.Dispatch(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("webhook requests = %d, want retry once and no duplicate", requests.Load())
	}
	filtered := event
	filtered.Status = notification.DownloadStart
	if err := d.Dispatch(context.Background(), filtered); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("filtered webhook sent: %d", requests.Load())
	}
}

func TestDispatcherHTTPNotificationsAndEmbyRefresh(t *testing.T) {
	var bark, telegram, serverChan, emby atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/push":
			bark.Add(1)
		case strings.Contains(r.URL.Path, "/sendMessage"):
			telegram.Add(1)
		case r.URL.Path == "/server-chan":
			serverChan.Add(1)
		case strings.HasPrefix(r.URL.Path, "/emby/Items/"):
			emby.Add(1)
		case r.URL.Path == "/bottoken/getUpdates":
			_, _ = w.Write([]byte(`{"result":[{"message":{"chat":{"id":1,"username":"one"}}},{"message":{"chat":{"id":1,"username":"one"}}},{"message":{"chat":{"id":2,"username":"two"}}}]}`))
			return
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	reader := configReader(t, nil)
	d := notification.New(reader, t.TempDir(), nil, nil)
	ani := model.Ani{ID: "http", Title: "Demo", Season: 1, Message: true}
	for _, cfg := range []map[string]any{
		{"notificationType": "BARK", "barkServerUrl": server.URL, "barkDeviceKeys": []any{"device"}, "barkUseMarkdown": true},
		{"notificationType": "TELEGRAM", "telegramApiHost": server.URL, "telegramBotToken": "token", "telegramChatId": "1", "telegramTopicId": -1},
		{"notificationType": "SERVER_CHAN", "serverChanType": "SERVER_CHAN_3", "serverChan3ApiUrl": server.URL + "/server-chan"},
	} {
		if err := d.Test(context.Background(), cfg, notification.Event{Ani: ani, Status: notification.DownloadStart, Text: "hello"}); err != nil {
			t.Fatal(err)
		}
	}
	updates, err := d.TelegramUpdates(context.Background(), map[string]any{"telegramApiHost": server.URL, "telegramBotToken": "token"})
	if err != nil || len(updates) != 2 {
		t.Fatalf("Telegram updates = %#v, err=%v", updates, err)
	}
	if err := d.Test(context.Background(), map[string]any{"notificationType": "EMBY_REFRESH", "embyHost": server.URL, "embyApiKey": "key", "embyRefreshViewIds": []any{"one", "two"}}, notification.Event{Ani: ani, Status: notification.DownloadEnd, Text: "done"}); err != nil {
		t.Fatal(err)
	}
	if bark.Load() != 1 || telegram.Load() != 1 || serverChan.Load() != 1 || emby.Load() != 2 {
		t.Fatalf("counts bark=%d telegram=%d serverChan=%d emby=%d", bark.Load(), telegram.Load(), serverChan.Load(), emby.Load())
	}
}

func TestTelegramUpdatesFillsMissingUsernameFromFirstAndLastName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/getUpdates" {
			t.Fatalf("Telegram path = %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"result":[{"message":{"chat":{"id":7,"type":"private","first_name":"Ada","last_name":"Lovelace"}}}]}`)
	}))
	defer server.Close()

	d := notification.New(configReader(t, nil), t.TempDir(), nil, nil)
	updates, err := d.TelegramUpdates(context.Background(), map[string]any{"telegramApiHost": server.URL, "telegramBotToken": "token"})
	if err != nil || len(updates) != 1 || updates[0]["username"] != "Ada Lovelace" || updates[0]["firstName"] != "Ada" || updates[0]["lastName"] != "Lovelace" {
		t.Fatalf("Telegram updates = %#v, err=%v", updates, err)
	}
	if _, present := updates[0]["first_name"]; present {
		t.Fatalf("Telegram update retained snake_case field: %#v", updates[0])
	}
}

func TestDispatcherSMTPAndShellBoundaries(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	mailReceived := make(chan struct{}, 1)
	go fakeSMTP(listener, mailReceived)
	m := configReader(t, nil)
	d := notification.New(m, t.TempDir(), nil, nil)
	mailHost, mailPort, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	_, _ = fmt.Sscanf(mailPort, "%d", &port)
	if err := d.Test(context.Background(), map[string]any{"notificationType": "MAIL", "mailFrom": "from@example.test", "mailSMTPHost": mailHost, "mailSMTPPort": port, "mailPassword": "pass", "mailAddressee": "to@example.test"}, notification.Event{Ani: model.Ani{Title: "Mail", Message: true}, Status: notification.DownloadEnd, Text: "mail"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-mailReceived:
	case <-time.After(time.Second):
		t.Fatal("SMTP message not received")
	}
	if err := d.Test(context.Background(), map[string]any{"notificationType": "SHELL", "shell": "test \"$ANI_RSS_TITLE\" = \"Shell\"", "aliveLimit": 2}, notification.Event{Ani: model.Ani{Title: "Shell", Message: true}, Status: notification.DownloadStart, Text: "shell"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Test(ctx, map[string]any{"notificationType": "SHELL", "shell": "sleep 1", "aliveLimit": 1}, notification.Event{Ani: model.Ani{Title: "Slow", Message: true}, Status: notification.DownloadStart, Text: "slow"}); err == nil {
		t.Fatal("shell timeout ignored")
	}
}

func fakeSMTP(listener net.Listener, received chan<- struct{}) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	_, _ = fmt.Fprint(connection, "220 fake smtp\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			_, _ = fmt.Fprint(connection, "250-fake\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(command, "AUTH"):
			_, _ = fmt.Fprint(connection, "235 authenticated\r\n")
		case strings.HasPrefix(command, "MAIL"), strings.HasPrefix(command, "RCPT"):
			_, _ = fmt.Fprint(connection, "250 ok\r\n")
		case command == "DATA":
			_, _ = fmt.Fprint(connection, "354 go\r\n")
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(line) == "." {
					break
				}
			}
			received <- struct{}{}
			_, _ = fmt.Fprint(connection, "250 queued\r\n")
		case command == "QUIT":
			_, _ = fmt.Fprint(connection, "221 bye\r\n")
			return
		default:
			_, _ = fmt.Fprint(connection, "250 ok\r\n")
		}
	}
}

func TestDispatcherFileMoveAndOpenListUploadRespectDeleteFlag(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Demo S01E01.mkv"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := configReader(t, nil)
	d := notification.New(m, root, nil, nil)
	ani := model.Ani{ID: "file", Title: "Demo", Season: 1, Message: true}
	target := filepath.Join(root, "target")
	if err := d.Test(context.Background(), map[string]any{"notificationType": "FILE_MOVE", "fileMoveTarget": target}, notification.Event{Ani: ani, Status: notification.DownloadEnd, Path: source, Text: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "Demo S01E01.mkv")); err != nil {
		t.Fatal(err)
	}
	var uploaded atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/fs/put" {
			uploaded.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	if err := d.Test(context.Background(), map[string]any{"notificationType": "OPEN_LIST_UPLOAD", "openListUploadHost": server.URL, "openListUploadApiKey": "key", "openListUploadPath": "/Media/${title}", "openListUploadDeleteLocalFile": true}, notification.Event{Ani: ani, Status: notification.DownloadEnd, Path: target, Text: "done"}); err != nil {
		t.Fatal(err)
	}
	if uploaded.Load() != 1 {
		t.Fatalf("uploaded = %d", uploaded.Load())
	}
	if _, err := os.Stat(filepath.Join(target, "Demo S01E01.mkv")); !os.IsNotExist(err) {
		t.Fatal("local file was not deleted after successful upload")
	}
}
