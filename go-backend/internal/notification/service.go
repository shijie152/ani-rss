// Package notification implements the notification and post-processing
// actions configured by the existing Vue UI.
package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

const (
	DownloadStart = "DOWNLOAD_START"
	DownloadEnd   = "DOWNLOAD_END"
	Omit          = "OMIT"
	Failure       = "ERROR"
	Completed     = "COMPLETED"
	Procrastinate = "PROCRASTINATING"
)

type Event struct {
	Ani      model.Ani
	Resource *model.Resource
	Metadata *model.Metadata
	Status   string
	Text     string
	Path     string
	Result   map[string]any
}

type Dispatcher struct {
	Config    appconfig.Reader
	ConfigDir string
	Client    *http.Client
	Logger    *slog.Logger
	mu        sync.Mutex
	seen      map[string]time.Time
}

// NewConfig returns the field-compatible object used by the existing
// notification editor when the user clicks “new”.
func NewConfig() map[string]any {
	return map[string]any{
		"enable": true, "retry": 3, "sort": 10, "comment": "", "notificationTemplate": "${notification}",
		"notificationType": "TELEGRAM", "statusList": []any{DownloadStart, Omit, Failure},
		"mailSMTPHost": "smtp.qq.com", "mailSMTPPort": 465, "mailFrom": "", "mailPassword": "", "mailSSLEnable": true, "mailTLSEnable": false, "mailAddressee": "", "mailImage": true,
		"serverChanType": "SERVER_CHAN", "serverChanSendKey": "", "serverChan3ApiUrl": "", "serverChanTitleAction": true,
		"telegramBotToken": "", "telegramChatId": "", "telegramTopicId": -1, "telegramApiHost": "https://api.telegram.org", "telegramImage": true, "telegramFormat": "",
		"webHookMethod": "POST", "webHookUrl": "", "webHookHeader": "", "webHookBody": "",
		// Java leaves embyHost nil in a newly-created NotificationConfig.
		// Gson omits that null field from /api/newNotification; keep the
		// field absent until the user enters a value so the UI-facing JSON
		// shape remains identical.
		"embyRefresh": false, "embyApiKey": "", "embyRefreshViewIds": []any{}, "embyDelayed": 0,
		"shell": "", "aliveLimit": 10,
		"fileMoveTarget": "/CD2/115/Media/番剧/${title}/Season ${season}", "fileMoveOvaTarget": "/CD2/115/Media/剧场版/${title}", "fileMoveDeleteOldEpisode": false, "fileMoveCopyModel": false,
		"openListUploadHost": "http://127.0.0.1:5244", "openListUploadApiKey": "", "openListUploadPath": "/115/Media/番剧/${title}/Season ${season}", "openListUploadOvaPath": "/115/Media/剧场版/${title}", "openListUploadDeleteLocalFile": false, "openListUploadDeleteOldEpisode": false,
		"barkServerUrl": "https://api.day.app", "barkDeviceKeys": []any{}, "barkGroup": "ani-rss", "barkUseMarkdown": false, "barkLevel": "active", "barkVolume": 5,
	}
}

// Test sends one explicit configuration without requiring it to be saved or
// enabled. This is the endpoint used by the current notification editor.
func (d *Dispatcher) Test(ctx context.Context, cfg map[string]any, event Event) error {
	return d.send(ctx, cfg, event)
}

// TelegramUpdates keeps the existing chat picker contract and returns unique
// chats from Telegram's getUpdates response.
func (d *Dispatcher) TelegramUpdates(ctx context.Context, cfg map[string]any) ([]map[string]any, error) {
	token := stringValue(cfg["telegramBotToken"])
	if token == "" {
		// The legacy controller treats an empty token as an unconfigured chat
		// picker and returns an empty list. Keep that default non-failing: the
		// notification editor uses this endpoint before Telegram is configured.
		return []map[string]any{}, nil
	}
	host := strings.TrimRight(stringValue(cfg["telegramApiHost"]), "/")
	if host == "" {
		host = "https://api.telegram.org"
	}
	data, _, _, err := doRequest(ctx, d.Client, http.MethodGet, host+"/bot"+token+"/getUpdates", nil, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Result []struct {
			Message *struct {
				Chat map[string]any `json:"chat"`
			} `json:"message"`
			MyChatMember *struct {
				Chat map[string]any `json:"chat"`
			} `json:"my_chat_member"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	result, seen := []map[string]any{}, map[string]bool{}
	for _, update := range response.Result {
		message := update.Message
		if message == nil {
			message = update.MyChatMember
		}
		if message == nil || message.Chat == nil {
			continue
		}
		chat := telegramChat(message.Chat)
		id := fmt.Sprint(chat["id"])
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, chat)
	}
	return result, nil
}

// telegramChat converts Telegram's snake_case wire shape to the Java DTO's
// camelCase JSON shape. Gson deserializes both aliases but serializes the Java
// field names, and the UI displays the synthesized username when Telegram
// does not provide one.
func telegramChat(raw map[string]any) map[string]any {
	chat := make(map[string]any, 5)
	copyField := func(output string, names ...string) {
		for _, name := range names {
			if value, ok := raw[name]; ok && value != nil {
				chat[output] = value
				return
			}
		}
	}
	copyField("id", "id")
	copyField("firstName", "first_name", "firstName")
	copyField("lastName", "last_name", "lastName")
	copyField("username", "username")
	copyField("type", "type")
	if stringValue(chat["username"]) == "" {
		chat["username"] = strings.TrimSpace(strings.Join([]string{stringValue(chat["firstName"]), stringValue(chat["lastName"])}, " "))
	}
	return chat
}

func New(config appconfig.Reader, configDir string, client *http.Client, logger *slog.Logger) *Dispatcher {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{Config: config, ConfigDir: configDir, Client: client, Logger: logger, seen: make(map[string]time.Time)}
}

// Dispatch applies status filtering, ordering, retry and process-lifetime
// deduplication to every configured notification action.
func (d *Dispatcher) Dispatch(ctx context.Context, event Event) error {
	if !event.Ani.Message {
		return nil
	}
	var failures []string
	for index, cfg := range d.configs() {
		if !boolValue(cfg["enable"], false) || !containsString(stringsValue(cfg["statusList"]), event.Status) {
			continue
		}
		key := eventKey(index, cfg, event)
		if d.wasSent(key) {
			continue
		}
		tries := intValue(cfg["retry"], 1)
		if tries < 1 {
			tries = 1
		}
		var err error
		for attempt := 0; attempt < tries; attempt++ {
			err = d.send(ctx, cfg, event)
			if err == nil {
				d.markSent(key)
				break
			}
			if attempt+1 < tries {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
				}
			}
		}
		if err != nil {
			name := stringValue(cfg["notificationType"])
			failures = append(failures, name+": "+err.Error())
			d.Logger.Warn("notification failed", "type", name, "status", event.Status, "error", err)
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (d *Dispatcher) configs() []map[string]any {
	if d.Config == nil {
		return nil
	}
	list, _ := d.Config.Snapshot()["notificationConfigList"].([]any)
	result := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if cfg, ok := item.(map[string]any); ok {
			result = append(result, cfg)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return intValue(result[i]["sort"], 0) < intValue(result[j]["sort"], 0) })
	return result
}

func eventKey(index int, cfg map[string]any, event Event) string {
	resource := ""
	if event.Resource != nil {
		resource = event.Resource.InfoHash + "\x00" + event.Resource.Title
	}
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", index, stringValue(cfg["notificationType"]), event.Ani.ID, event.Status, resource)
}

// notificationDedupTTL is how long a sent notification is suppressed, and
// notificationDedupCap bounds the dedup map. The map only ever grew before:
// markSent appended keys and nothing removed them, so a long-running process
// leaked one entry per notification forever. Entries older than the TTL are
// reaped on write, and the map is hard-capped as a second defence.
const (
	notificationDedupTTL = 24 * time.Hour
	notificationDedupCap = 4096
)

func (d *Dispatcher) wasSent(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.seen[key]) < notificationDedupTTL
}

func (d *Dispatcher) markSent(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	d.seen[key] = now
	// Reap expired entries so the map cannot grow without bound.
	for k, t := range d.seen {
		if now.Sub(t) >= notificationDedupTTL {
			delete(d.seen, k)
		}
	}
	if len(d.seen) > notificationDedupCap {
		// Hard cap: evict the oldest entries when a pathological burst exceeds it.
		var oldestKey string
		var oldest time.Time
		for k, t := range d.seen {
			if oldestKey == "" || t.Before(oldest) {
				oldestKey, oldest = k, t
			}
		}
		delete(d.seen, oldestKey)
	}
}

func (d *Dispatcher) send(ctx context.Context, cfg map[string]any, event Event) error {
	text := renderTemplate(d.Config, cfg, event)
	switch strings.ToUpper(stringValue(cfg["notificationType"])) {
	case "WEB_HOOK":
		return d.webhook(ctx, cfg, event, text)
	case "BARK":
		return d.bark(ctx, cfg, text)
	case "TELEGRAM":
		return d.telegram(ctx, cfg, text)
	case "SERVER_CHAN":
		return d.serverChan(ctx, cfg, event, text)
	case "MAIL":
		return d.mail(ctx, cfg, text)
	case "SHELL":
		return d.shell(ctx, cfg, event, text)
	case "FILE_MOVE":
		return d.fileMove(cfg, event)
	case "OPEN_LIST_UPLOAD":
		return d.openListUpload(ctx, cfg, event)
	case "EMBY_REFRESH":
		return d.embyRefresh(ctx, cfg)
	case "SYSTEM":
		d.Logger.Info("system notification", "title", event.Ani.Title, "text", text)
		return nil
	default:
		return fmt.Errorf("不支持的通知类型: %s", cfg["notificationType"])
	}
}

func renderTemplate(global appconfig.Reader, cfg map[string]any, event Event) string {
	template := stringValue(cfg["notificationTemplate"])
	if template == "" && global != nil {
		template = appconfig.String(global.Snapshot(), "notificationTemplate")
	}
	if template == "" {
		template = "${text}"
	}
	episode := 0.0
	if event.Resource != nil {
		episode = event.Resource.Episode
	}
	if episode == 0 {
		episode = episodeFromText(event.Text)
	}
	episodeFormat := strconv.Itoa(int(episode))
	if episode > 0 {
		episodeFormat = fmt.Sprintf("%02d", int(episode))
		if episode != float64(int(episode)) {
			episodeFormat += ".5"
		}
	}
	values := map[string]string{
		"text": event.Text, "title": event.Ani.Title, "jpTitle": event.Ani.JPTitle, "season": strconv.Itoa(event.Ani.Season), "seasonFormat": fmt.Sprintf("%02d", event.Ani.Season),
		"episode": strconv.FormatFloat(episode, 'f', -1, 64), "episodeFormat": episodeFormat, "year": strconv.Itoa(event.Ani.Year), "month": strconv.Itoa(event.Ani.Month), "date": strconv.Itoa(event.Ani.Date),
		"subgroup": event.Ani.Subgroup, "downloadPath": event.Path, "action": action(event.Status), "emoji": emoji(event.Status), "comment": stringValue(cfg["comment"]),
		"tmdbid": mapValueString(event.Ani.TMDB, "id"), "notification": event.Text,
	}
	if values["comment"] == "" {
		values["comment"] = "无备注"
	}
	for key, value := range values {
		template = strings.ReplaceAll(template, "${"+key+"}", value)
	}
	return strings.TrimSpace(template)
}

var episodeFromTextPattern = regexp.MustCompile(`(?i)(?:s\d+e|e|ep|第|[- ])\s*(\d+(?:\.5)?)`)

func episodeFromText(text string) float64 {
	match := episodeFromTextPattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return 0
	}
	value, _ := strconv.ParseFloat(match[1], 64)
	return value
}
func action(status string) string {
	return map[string]string{DownloadStart: "开始下载", DownloadEnd: "下载完成", Omit: "缺少集数", Failure: "发生错误", Completed: "订阅完结", Procrastinate: "摸鱼检测"}[status]
}
func emoji(status string) string {
	return map[string]string{DownloadStart: "🎈", DownloadEnd: "🎉", Omit: "⛔", Failure: "❌", Completed: "🎊", Procrastinate: "🐟"}[status]
}

func (d *Dispatcher) webhook(ctx context.Context, cfg map[string]any, event Event, text string) error {
	address := strings.ReplaceAll(stringValue(cfg["webHookUrl"]), "${notification}", text)
	address = strings.ReplaceAll(address, "${message}", text)
	if address == "" {
		return errors.New("webhook URL 为空")
	}
	body := strings.ReplaceAll(stringValue(cfg["webHookBody"]), "${notification}", text)
	body = strings.ReplaceAll(body, "${message}", text)
	body = strings.ReplaceAll(body, "${image}", event.Ani.Image)
	method := strings.ToUpper(stringValue(cfg["webHookMethod"]))
	if method == "" {
		method = http.MethodPost
	}
	_, _, _, err := doRequest(ctx, d.Client, method, address, parseHeaders(stringValue(cfg["webHookHeader"])), []byte(body))
	return err
}

func (d *Dispatcher) bark(ctx context.Context, cfg map[string]any, text string) error {
	host := strings.TrimRight(stringValue(cfg["barkServerUrl"]), "/")
	if host == "" {
		return errors.New("Bark ServerUrl 为空")
	}
	devices := stringsValue(cfg["barkDeviceKeys"])
	if len(devices) == 0 {
		return errors.New("Bark DeviceKeys 为空")
	}
	payload := map[string]any{"device_keys": devices, "title": "ani-rss", "subtitle": text, "group": stringValue(cfg["barkGroup"]), "level": stringValue(cfg["barkLevel"]), "volume": intValue(cfg["barkVolume"], 0)}
	if boolValue(cfg["barkUseMarkdown"], false) {
		payload["markdown"] = text
	} else {
		payload["body"] = text
	}
	_, _, _, err := jsonRequest(ctx, d.Client, host+"/push", payload)
	return err
}

func (d *Dispatcher) telegram(ctx context.Context, cfg map[string]any, text string) error {
	token, chat := stringValue(cfg["telegramBotToken"]), stringValue(cfg["telegramChatId"])
	if token == "" || chat == "" {
		return errors.New("Telegram 参数不完整")
	}
	host := strings.TrimRight(stringValue(cfg["telegramApiHost"]), "/")
	if host == "" {
		host = "https://api.telegram.org"
	}
	payload := map[string]any{"chat_id": chat, "text": text}
	if topic := intValue(cfg["telegramTopicId"], -1); topic > -1 {
		payload["message_thread_id"] = topic
	}
	if format := stringValue(cfg["telegramFormat"]); format != "" {
		payload["parse_mode"] = format
	}
	_, _, _, err := jsonRequest(ctx, d.Client, host+"/bot"+token+"/sendMessage", payload)
	return err
}

func (d *Dispatcher) serverChan(ctx context.Context, cfg map[string]any, event Event, text string) error {
	address := stringValue(cfg["serverChan3ApiUrl"])
	if strings.ToUpper(stringValue(cfg["serverChanType"])) != "SERVER_CHAN_3" {
		key := stringValue(cfg["serverChanSendKey"])
		if key == "" {
			return errors.New("ServerChan sendKey 为空")
		}
		address = "https://sctapi.ftqq.com/" + key + ".send"
	}
	if address == "" {
		return errors.New("ServerChan apiUrl 为空")
	}
	title := text
	if boolValue(cfg["serverChanTitleAction"], true) {
		title = action(event.Status) + "#" + event.Ani.Title
	}
	_, _, _, err := jsonRequest(ctx, d.Client, address, map[string]any{"title": title, "desp": text})
	return err
}

func (d *Dispatcher) mail(_ context.Context, cfg map[string]any, text string) error {
	from, host, password, to := stringValue(cfg["mailFrom"]), stringValue(cfg["mailSMTPHost"]), stringValue(cfg["mailPassword"]), stringValue(cfg["mailAddressee"])
	if from == "" || host == "" || password == "" || to == "" {
		return errors.New("SMTP 参数不完整")
	}
	port := intValue(cfg["mailSMTPPort"], 25)
	if port < 1 || port > 65535 {
		return errors.New("SMTP 端口异常")
	}
	body := "MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	message := []byte("To: " + to + "\r\nFrom: " + from + "\r\nSubject: ani-rss\r\n" + body)
	auth := smtp.PlainAuth("", from, password, host)
	if boolValue(cfg["mailSSLEnable"], false) {
		return sendSMTPOverTLS(host, port, from, to, auth, message)
	}
	return smtp.SendMail(host+":"+strconv.Itoa(port), auth, from, []string{to}, message)
}

func sendSMTPOverTLS(host string, port int, from, to string, auth smtp.Auth, message []byte) error {
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", host+":"+strconv.Itoa(port), &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer client.Close()
	if err = client.Auth(auth); err != nil {
		return err
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (d *Dispatcher) shell(ctx context.Context, cfg map[string]any, event Event, text string) error {
	commandConfig := make(map[string]any, len(cfg)+1)
	for key, value := range cfg {
		commandConfig[key] = value
	}
	commandConfig["notificationTemplate"] = stringValue(cfg["shell"])
	command := renderTemplate(d.Config, commandConfig, event)
	if command == "" {
		return errors.New("shell 为空")
	}
	seconds := intValue(cfg["aliveLimit"], 10)
	if seconds < 1 {
		seconds = 1
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	shell, args := "sh", []string{"-c", command}
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		shell, args = comspec, []string{"/c", command}
	}
	cmd := exec.CommandContext(runCtx, shell, args...)
	cmd.Env = append(os.Environ(), "ANI_RSS_NOTIFICATION="+text, "ANI_RSS_TITLE="+event.Ani.Title)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("shell exit: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (d *Dispatcher) fileMove(cfg map[string]any, event Event) error {
	if event.Path == "" {
		return errors.New("文件移动源路径为空")
	}
	target := stringValue(cfg["fileMoveTarget"])
	if event.Ani.OVA {
		target = stringValue(cfg["fileMoveOvaTarget"])
	}
	target = expandAniPath(target, event.Ani)
	if target == "" {
		return errors.New("文件移动目标路径为空")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(event.Path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !mediaFile(entry.Name()) {
			continue
		}
		source, destination := filepath.Join(event.Path, entry.Name()), filepath.Join(target, entry.Name())
		if _, err := os.Stat(destination); err == nil {
			continue
		}
		if boolValue(cfg["fileMoveCopyModel"], false) {
			if err := copyFile(source, destination); err != nil {
				return err
			}
			continue
		}
		if err := os.Rename(source, destination); err != nil {
			if err := copyFile(source, destination); err != nil {
				return err
			}
			if err := os.Remove(source); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dispatcher) openListUpload(ctx context.Context, cfg map[string]any, event Event) error {
	if event.Path == "" {
		return errors.New("OpenList 上传源路径为空")
	}
	host, token := strings.TrimRight(stringValue(cfg["openListUploadHost"]), "/"), stringValue(cfg["openListUploadApiKey"])
	if host == "" || token == "" {
		return errors.New("OpenList 上传参数不完整")
	}
	target := stringValue(cfg["openListUploadPath"])
	if event.Ani.OVA {
		target = stringValue(cfg["openListUploadOvaPath"])
	}
	target = expandAniPath(target, event.Ani)
	entries, err := os.ReadDir(event.Path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !mediaFile(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(event.Path, entry.Name()))
		if err != nil {
			return err
		}
		headers := http.Header{"Authorization": []string{token}, "As-Task": []string{"false"}, "File-Path": []string{urlEncodePath(target + "/" + entry.Name())}, "Content-Type": []string{"application/octet-stream"}}
		if _, _, _, err := doRequest(ctx, d.Client, http.MethodPut, host+"/api/fs/put", headers, data); err != nil {
			return err
		}
		if boolValue(cfg["openListUploadDeleteLocalFile"], false) {
			if err := os.Remove(filepath.Join(event.Path, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dispatcher) embyRefresh(ctx context.Context, cfg map[string]any) error {
	host, token := strings.TrimRight(stringValue(cfg["embyHost"]), "/"), stringValue(cfg["embyApiKey"])
	if host == "" || token == "" {
		return errors.New("Emby 参数不完整")
	}
	if delay := intValue(cfg["embyDelayed"], 0); delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(delay) * time.Second):
		}
	}
	ids := stringsValue(cfg["embyRefreshViewIds"])
	if len(ids) == 0 {
		return errors.New("Emby 未选择媒体库")
	}
	for _, id := range ids {
		query := "Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&ReplaceAllImages=false&ReplaceAllMetadata=false"
		headers := http.Header{"X-Emby-Token": []string{token}}
		if _, _, _, err := doRequest(ctx, d.Client, http.MethodPost, host+"/emby/Items/"+id+"/Refresh?"+query, headers, nil); err != nil {
			return err
		}
	}
	return nil
}

func doRequest(ctx context.Context, client *http.Client, method, address string, headers http.Header, body []byte) ([]byte, int, http.Header, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, response.StatusCode, response.Header, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return data, response.StatusCode, response.Header, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, response.StatusCode, response.Header, nil
}
func jsonRequest(ctx context.Context, client *http.Client, address string, payload any) ([]byte, int, http.Header, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, nil, err
	}
	return doRequest(ctx, client, http.MethodPost, address, http.Header{"Content-Type": []string{"application/json"}}, data)
}
func parseHeaders(value string) http.Header {
	result := make(http.Header)
	for _, line := range strings.Split(value, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			result.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
	return result
}
func expandAniPath(value string, ani model.Ani) string {
	for key, item := range map[string]string{"${title}": ani.Title, "${season}": strconv.Itoa(ani.Season), "${year}": strconv.Itoa(ani.Year), "${jpTitle}": ani.JPTitle} {
		value = strings.ReplaceAll(value, key, item)
	}
	return filepath.Clean(value)
}
func mediaFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".mov", ".flv", ".wmv", ".ts", ".m4v", ".srt", ".ass", ".ssa", ".vtt":
		return true
	}
	return false
}
func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
func urlEncodePath(value string) string { return strings.ReplaceAll(value, " ", "%20") }
func stringValue(value any) string {
	if typed, ok := value.(string); ok {
		return strings.TrimSpace(typed)
	}
	return ""
}
func boolValue(value any, fallback bool) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	return fallback
}
func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}
func stringsValue(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		return append(result, typed...)
	case []any:
		for _, item := range typed {
			if item := stringValue(item); item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
func mapValueString(value map[string]any, key string) string { return stringValue(value[key]) }
