package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/httpclient"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

type logBuffer struct {
	mu    sync.RWMutex
	max   int
	items []model.Log
}

func newLogBuffer(max int) *logBuffer {
	if max < 1 {
		max = 256
	}
	return &logBuffer{max: max, items: make([]model.Log, 0, max)}
}
func (b *logBuffer) Enabled(context.Context, slog.Level) bool { return true }
func (b *logBuffer) WithAttrs(attrs []slog.Attr) slog.Handler {
	return b
}
func (b *logBuffer) WithGroup(string) slog.Handler { return b }
func (b *logBuffer) Handle(_ context.Context, record slog.Record) error {
	entry := model.Log{TS: record.Time.UnixMilli(), Message: record.Message, Level: strings.ToUpper(record.Level.String()), LoggerName: "ani-rss", ThreadName: ""}
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "logger" {
			entry.LoggerName = attr.Value.String()
		}
		return true
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = append(b.items, entry)
	if len(b.items) > b.max {
		b.items = append([]model.Log(nil), b.items[len(b.items)-b.max:]...)
	}
	return nil
}
func (b *logBuffer) List() []model.Log {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]model.Log{}, b.items...)
}
func (b *logBuffer) Clear() { b.mu.Lock(); b.items = b.items[:0]; b.mu.Unlock() }

type teeHandler struct {
	first  slog.Handler
	second slog.Handler
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.first.Enabled(ctx, level) || h.second.Enabled(ctx, level)
}
func (h *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	firstErr := h.first.Handle(ctx, record)
	secondErr := h.second.Handle(ctx, record)
	if firstErr != nil {
		return firstErr
	}
	return secondErr
}
func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{first: h.first.WithAttrs(attrs), second: h.second.WithAttrs(attrs)}
}
func (h *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{first: h.first.WithGroup(name), second: h.second.WithGroup(name)}
}

type zipLimit struct {
	entries int
	bytes   int64
}

const (
	maxUploadBytes      = 64 << 20
	maxZipEntryBytes    = 64 << 20
	maxZipExpandedBytes = 128 << 20
	maxZipEntries       = 10000
	maxImageBytes       = 32 << 20
)

func (a *App) logs(w http.ResponseWriter, _ *http.Request) {
	if a.logBuffer == nil {
		writeResult(w, http.StatusOK, []model.Log{}, "success")
		return
	}
	writeResult(w, http.StatusOK, a.logBuffer.List(), "success")
}

func (a *App) clearLogs(w http.ResponseWriter, _ *http.Request) {
	if a.logBuffer != nil {
		a.logBuffer.Clear()
	}
	a.logger.Info("清理日志")
	writeResult(w, http.StatusOK, nil, "success")
}

func (a *App) downloadLogs(w http.ResponseWriter, _ *http.Request) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	logsRoot := filepath.Join(a.configDir, "logs")
	_ = filepath.WalkDir(logsRoot, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.ToLower(filepath.Ext(entry.Name())) != ".log" {
			return nil
		}
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil
		}
		relative, relErr := filepath.Rel(logsRoot, filePath)
		if relErr != nil {
			return nil
		}
		writer, createErr := archive.Create(filepath.ToSlash(relative))
		if createErr == nil {
			_, _ = writer.Write(data)
		}
		return nil
	})
	_ = archive.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `inline; filename="logs.zip"`)
	w.Header().Set("Content-Length", strconv.Itoa(output.Len()))
	_, _ = w.Write(output.Bytes())
}

func (a *App) exportConfig(w http.ResponseWriter, _ *http.Request) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	writeEntry := func(name string, data []byte) {
		writer, err := archive.Create(name)
		if err == nil {
			_, _ = writer.Write(data)
		}
	}
	if config, err := a.store.LoadConfig(); err == nil {
		if data, marshalErr := json.MarshalIndent(config, "", "  "); marshalErr == nil {
			writeEntry("config.v2.json", append(data, '\n'))
		}
	}
	if items, err := a.store.LoadSubscriptions(); err == nil {
		if data, marshalErr := json.MarshalIndent(items, "", "  "); marshalErr == nil {
			writeEntry("ani.v2.json", append(data, '\n'))
		}
	}
	if resources, err := a.history.LoadResources(); err == nil {
		if data, marshalErr := json.MarshalIndent(resources, "", "  "); marshalErr == nil {
			writeEntry("resources.v2.json", append(data, '\n'))
		}
	}
	if a.tasks != nil {
		if tasks, err := a.tasks.LoadTasks(); err == nil {
			if data, marshalErr := json.MarshalIndent(tasks, "", "  "); marshalErr == nil {
				writeEntry("tasks.v2.json", append(data, '\n'))
			}
		}
	}
	for _, root := range []string{"files", "torrents"} {
		full := filepath.Join(a.configDir, root)
		_ = filepath.WalkDir(full, func(filePath string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				return nil
			}
			data, readErr := os.ReadFile(filePath)
			if readErr != nil {
				return nil
			}
			relative, relErr := filepath.Rel(a.configDir, filePath)
			if relErr != nil {
				return nil
			}
			writeEntry(filepath.ToSlash(relative), data)
			return nil
		})
	}
	_ = archive.Close()
	w.Header().Set("Content-Type", "application/zip")
	version := strings.TrimSpace(a.version)
	if version == "" {
		version = strings.TrimSpace(appconfig.String(a.config.Snapshot(), "version"))
	}
	if version == "" {
		version = "dev"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="ani-rss.backup.%s.zip"`, version))
	w.Header().Set("Content-Length", strconv.Itoa(output.Len()))
	_, _ = w.Write(output.Bytes())
}

func (a *App) importConfig(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if isRequestTooLarge(err) {
			writeResult(w, http.StatusRequestEntityTooLarge, nil, "备份文件过大")
		} else {
			writeResult(w, http.StatusInternalServerError, nil, "Content-Type is not supported")
		}
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil || header == nil || strings.ToLower(filepath.Ext(header.Filename)) != ".zip" {
		writeResult(w, http.StatusInternalServerError, nil, "导入格式异常")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil || len(data) > maxUploadBytes {
		writeResult(w, http.StatusRequestEntityTooLarge, nil, "备份文件过大")
		return
	}
	if err := a.restoreZip(data); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	if err := a.config.Reload(); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "重新加载配置失败: "+err.Error())
		return
	}
	if err := a.subscriptions.Reload(); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "重新加载订阅失败: "+err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "导入成功")
}

func (a *App) restoreZip(data []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("备份文件损坏")
	}
	limit := zipLimit{}
	type pendingFile struct {
		name string
		data []byte
	}
	pending := []pendingFile{}
	var config model.Config
	var items []model.Ani
	var resources []model.Resource
	var tasks []model.Torrent
	hasConfig, hasItems, hasResources, hasTasks := false, false, false, false
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		limit.entries++
		if limit.entries > maxZipEntries || entry.UncompressedSize64 > maxZipEntryBytes {
			return errors.New("备份内容超出限制")
		}
		name, ok := safeArchiveName(entry.Name)
		if !ok || !allowedBackupName(name) {
			return errors.New("备份包含不安全文件")
		}
		limit.bytes += int64(entry.UncompressedSize64)
		if limit.bytes > maxZipExpandedBytes {
			return errors.New("备份解压内容过大")
		}
		input, openErr := entry.Open()
		if openErr != nil {
			return openErr
		}
		content, readErr := io.ReadAll(io.LimitReader(input, maxZipEntryBytes+1))
		_ = input.Close()
		if readErr != nil || len(content) > maxZipEntryBytes {
			return errors.New("备份文件读取失败")
		}
		switch name {
		case "config.v2.json":
			if err := json.Unmarshal(content, &config); err != nil || config == nil {
				return errors.New("配置备份格式异常")
			}
			hasConfig = true
		case "ani.v2.json":
			if err := json.Unmarshal(content, &items); err != nil || items == nil {
				return errors.New("订阅备份格式异常")
			}
			hasItems = true
		case "resources.v2.json":
			if err := json.Unmarshal(content, &resources); err != nil || resources == nil {
				return errors.New("资源历史备份格式异常")
			}
			hasResources = true
		case "tasks.v2.json":
			if err := json.Unmarshal(content, &tasks); err != nil || tasks == nil {
				return errors.New("下载任务备份格式异常")
			}
			hasTasks = true
		}
		pending = append(pending, pendingFile{name: name, data: content})
	}
	if len(pending) == 0 {
		return errors.New("备份为空")
	}
	currentConfig, configErr := a.store.LoadConfig()
	currentItems, itemsErr := a.store.LoadSubscriptions()
	currentResources, resourcesErr := a.history.LoadResources()
	currentTasks := []model.Torrent{}
	var tasksErr error
	if a.tasks != nil {
		currentTasks, tasksErr = a.tasks.LoadTasks()
	}
	if configErr != nil || itemsErr != nil || resourcesErr != nil || tasksErr != nil {
		return errors.New("读取当前应用状态失败")
	}
	if !hasConfig {
		config = currentConfig
	} else {
		config = appconfig.MergeConfig(currentConfig, config)
	}
	if !hasItems {
		items = currentItems
	}
	if !hasResources {
		resources = currentResources
	}
	if !hasTasks {
		tasks = currentTasks
	}
	if err := subscription.ValidateItems(items); err != nil {
		return fmt.Errorf("订阅备份校验失败: %w", err)
	}
	if err := appconfig.Normalize(config); err != nil {
		return fmt.Errorf("配置备份校验失败: %w", err)
	}
	if replacer, ok := a.store.(interface {
		ReplaceStateWithTasks(model.Config, []model.Ani, []model.Resource, []model.Torrent) error
	}); ok {
		if err := replacer.ReplaceStateWithTasks(config, items, resources, tasks); err != nil {
			return err
		}
	} else if replacer, ok := a.store.(interface {
		ReplaceState(model.Config, []model.Ani, []model.Resource) error
	}); ok {
		if err := replacer.ReplaceState(config, items, resources); err != nil {
			return err
		}
	} else {
		return errors.New("当前存储不支持原子恢复")
	}
	// The Java import contract replaces the downloader cache as well as the
	// JSON state. This is explicit user-requested replacement, scoped to the
	// application-owned torrents directory.
	if err := os.RemoveAll(filepath.Join(a.configDir, "torrents")); err != nil {
		return err
	}
	for _, item := range pending {
		if item.name == "config.v2.json" || item.name == "ani.v2.json" || item.name == "resources.v2.json" || item.name == "tasks.v2.json" {
			continue
		}
		target := filepath.Join(a.configDir, filepath.FromSlash(item.name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, item.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func safeArchiveName(value string) (string, bool) {
	value = strings.ReplaceAll(value, "\\", "/")
	clean := path.Clean(value)
	if clean == "." || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, '\x00') {
		return "", false
	}
	return clean, true
}
func allowedBackupName(name string) bool {
	base := filepath.Base(name)
	return base == "config.v2.json" || base == "ani.v2.json" || base == "resources.v2.json" || base == "tasks.v2.json" || strings.HasPrefix(name, "files/") || strings.HasPrefix(name, "torrents/") || strings.HasPrefix(name, "webui/")
}

func (a *App) clearCache(w http.ResponseWriter, _ *http.Request) {
	var removed int64
	_ = filepath.WalkDir(filepath.Join(a.configDir, "img"), func(filePath string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if data, readErr := os.ReadFile(filePath); readErr == nil {
			removed += int64(len(data))
		}
		_ = os.Remove(filePath)
		return nil
	})
	used := map[string]bool{}
	for _, item := range a.subscriptions.Items() {
		if item.Cover == "" {
			continue
		}
		used[filepath.Clean(filepath.Join(a.configDir, "files", filepath.FromSlash(item.Cover)))] = true
	}
	filesRoot := filepath.Join(a.configDir, "files")
	_ = filepath.WalkDir(filesRoot, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || used[filepath.Clean(filePath)] {
			return nil
		}
		if data, readErr := os.ReadFile(filePath); readErr == nil {
			removed += int64(len(data))
		}
		_ = os.Remove(filePath)
		return nil
	})
	writeResult(w, http.StatusOK, nil, fmt.Sprintf("清理完成, 共清理 %s", formatBytes(removed)))
}

func (a *App) trackersUpdate(w http.ResponseWriter, r *http.Request) {
	var cfg model.Config
	if err := decodeJSON(r, &cfg); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "配置格式异常")
		return
	}
	urls := strings.FieldsFunc(appconfig.String(cfg, "trackersUpdateUrls"), func(r rune) bool { return r == '\n' || r == '\r' })
	if len(urls) == 0 || allBlank(urls) {
		writeResult(w, http.StatusInternalServerError, nil, "Trackers更新地址 为空")
		return
	}
	trackers := map[string]bool{}
	client, err := httpclientForConfig(cfg)
	if err == nil {
		for _, address := range urls {
			address = strings.TrimSpace(address)
			if address == "" {
				continue
			}
			response, requestErr := client.Get(address)
			if requestErr != nil {
				err = requestErr
				break
			}
			contentType := response.Header.Get("Content-Type")
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 || contentType == "" || !strings.Contains(strings.ToLower(contentType), "text/plain") || readErr != nil {
				err = fmt.Errorf("更新 trackers 失败: %s", address)
				break
			}
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.Trim(strings.TrimSpace(line), `"`)
				lower := strings.ToLower(line)
				if line != "" && (strings.HasPrefix(lower, "udp://") || strings.HasPrefix(lower, "wss://") || strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) {
					trackers[line] = true
				}
			}
		}
	}
	if err == nil && len(trackers) == 0 {
		err = errors.New("获取到 0 个 trackers")
	}
	if err == nil {
		adapterCfg := a.config.Snapshot()
		adapter, adapterErr := downloader.New(adapterCfg, client)
		if adapterErr == nil {
			err = adapter.Login(r.Context())
			if err == nil {
				values := make([]string, 0, len(trackers))
				for tracker := range trackers {
					values = append(values, tracker)
				}
				sort.Strings(values)
				err = adapter.UpdateTrackers(r.Context(), values)
			}
		} else {
			err = adapterErr
		}
	}
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "success")
}

func httpclientForConfig(cfg model.Config) (*http.Client, error) {
	return httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
}

func (a *App) proxyImage(w http.ResponseWriter, r *http.Request) {
	encodedURL, queryErr := requiredQuery(r, "imgUrl")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	encoded := strings.ReplaceAll(encodedURL, " ", "+")
	imageURL, err := decodeBase64Param(encoded)
	if err != nil {
		writeResult(w, http.StatusForbidden, nil, "图片地址格式异常")
		return
	}
	target, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil || !safeRemoteURL(target) {
		writeResult(w, http.StatusForbidden, nil, "禁止访问图片地址")
		return
	}
	ext := strings.ToLower(filepath.Ext(target.Path))
	if len(ext) > 8 {
		ext = ""
	}
	if ext == "" {
		ext = ".img"
	}
	hash := fmt.Sprintf("%x", md5.Sum([]byte(imageURL)))
	cachePath := filepath.Join(a.configDir, "img", string(hash[0]), hash+ext)
	if info, statErr := os.Stat(cachePath); statErr != nil || !info.Mode().IsRegular() {
		client, clientErr := httpclientForConfig(a.config.Snapshot())
		if clientErr != nil {
			writeResult(w, http.StatusInternalServerError, nil, clientErr.Error())
			return
		}
		request, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
		if requestErr != nil {
			writeResult(w, http.StatusForbidden, nil, "图片地址格式异常")
			return
		}
		response, requestErr := client.Do(request)
		if requestErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			if response != nil {
				response.Body.Close()
			}
			writeResult(w, http.StatusBadGateway, nil, "图片下载失败")
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
		response.Body.Close()
		if readErr != nil || len(data) > maxImageBytes {
			writeResult(w, http.StatusRequestEntityTooLarge, nil, "图片过大")
			return
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			writeResult(w, http.StatusInternalServerError, nil, err.Error())
			return
		}
		if err := os.WriteFile(cachePath, data, 0o644); err != nil {
			writeResult(w, http.StatusInternalServerError, nil, err.Error())
			return
		}
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		writeResult(w, http.StatusNotFound, nil, "图片不存在")
		return
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Cache-Control", "public, max-age=2592000")
	w.Header().Set("Content-Type", contentType)
	file, openErr := os.Open(cachePath)
	if openErr != nil {
		writeResult(w, http.StatusNotFound, nil, "图片不存在")
		return
	}
	defer file.Close()
	http.ServeContent(w, r, filepath.Base(cachePath), info.ModTime(), file)
}

func safeRemoteURL(value *url.URL) bool {
	if value == nil || (value.Scheme != "http" && value.Scheme != "https") || value.Hostname() == "" {
		return false
	}
	host := strings.ToLower(value.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	return true
}

func (a *App) calendar(w http.ResponseWriter, _ *http.Request) {
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//ani-rss//CN", "CALSCALE:GREGORIAN", "METHOD:PUBLISH", "X-WR-CALNAME:ani-rss", "X-WR-TIMEZONE:Asia/Shanghai", "X-WR-CALDESC:ani-rss 动漫订阅日历"}
	for _, item := range a.subscriptions.Items() {
		if !item.Enable {
			continue
		}
		start := parseCalendarDate(item)
		end := start.AddDate(0, 0, 1)
		lines = append(lines, "BEGIN:VEVENT", "UID:ani-rss-"+icsEscape(item.ID)+"@ani-rss", "DTSTAMP:"+time.Now().UTC().Format("20060102T150405Z"), "DTSTART;VALUE=DATE:"+start.Format("20060102"), "DTEND;VALUE=DATE:"+end.Format("20060102"), "SUMMARY:"+icsEscape(fmt.Sprintf("%s 第%d季", item.Title, item.Season)), "STATUS:CONFIRMED", "TRANSP:TRANSPARENT", "CATEGORIES:动漫,第"+strconv.Itoa(item.Season)+"季,"+map[bool]string{true: "剧场版/OVA", false: "TV"}[item.OVA], "DESCRIPTION:"+icsEscape(fmt.Sprintf("标题: %s\\n类型: %s\\n季度: %d\\nBangumi: %s", item.Title, map[bool]string{true: "剧场版/OVA", false: "TV"}[item.OVA], item.Season, item.BGMURL)))
		if item.BGMURL != "" {
			lines = append(lines, "URL:"+icsEscape(item.BGMURL))
		}
		if !item.OVA {
			until := start.AddDate(0, 0, 30)
			if item.TotalEpisodeNumber > 0 {
				until = start.AddDate(0, 0, 7*(item.TotalEpisodeNumber-1))
			}
			lines = append(lines, "RRULE:FREQ=WEEKLY;BYDAY="+strings.ToUpper(start.Weekday().String()[:2])+";UNTIL="+until.Format("20060102"))
		}
		lines = append(lines, "END:VEVENT")
	}
	lines = append(lines, "END:VCALENDAR")
	w.Header().Set("Content-Type", "text/calendar;charset=UTF-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ani-rss-calendar.ics"`)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, strings.Join(lines, "\r\n")+"\r\n")
}
func parseCalendarDate(item model.Ani) time.Time {
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
		if value, err := time.ParseInLocation(layout, item.ReleaseDate, time.Local); err == nil {
			return value
		}
	}
	return time.Now()
}
func icsEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, ";", `\;`)
	value = strings.ReplaceAll(value, ",", `\,`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func (a *App) about(w http.ResponseWriter, _ *http.Request) {
	data := map[string]any{"version": a.version, "latest": "", "update": false, "autoUpdate": false, "downloadUrl": "", "sha256": "", "size": int64(0), "formatSize": "0 MiB", "markdownBody": "", "date": nil}
	if a.updater != nil {
		if info, err := a.updater.Check(context.Background()); err == nil {
			data["latest"], data["update"], data["autoUpdate"] = info.Version, info.Update, info.AutoUpdate
			data["downloadUrl"], data["sha256"], data["size"] = info.URL, info.SHA256, info.Size
			data["formatSize"], data["markdownBody"], data["date"] = info.FormatSize, info.Body, info.Date
		} else {
			a.logger.Warn("release check failed", "error", err)
		}
	}
	writeResult(w, http.StatusOK, data, "success")
}
func (a *App) update(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		writeResult(w, http.StatusOK, nil, "当前版本无需更新")
		return
	}
	info, err := a.updater.Check(r.Context())
	if err != nil {
		writeResult(w, http.StatusInternalServerError, nil, "检测更新失败")
		return
	}
	if !info.Update {
		writeResult(w, http.StatusOK, nil, "当前版本无需更新")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.updater.Install(ctx, info); err != nil {
			a.logger.Error("update failed", "error", err)
			return
		}
		if runtime.GOOS == "windows" {
			return
		}
		// Start the replaced binary after the HTTP response has been returned;
		// the old process is then safe to terminate and the UI can reconnect.
		time.Sleep(500 * time.Millisecond)
		if executable, executableErr := os.Executable(); executableErr == nil {
			if command := exec.Command(executable, os.Args[1:]...); command.Start() == nil {
				os.Exit(0)
			}
		}
	}()
	writeResult(w, http.StatusOK, nil, "更新成功, 正在重启...")
}
func (a *App) stop(w http.ResponseWriter, r *http.Request) {
	statusText, queryErr := requiredQueryWithType(r, "status", "Integer")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	status, err := strconv.Atoi(statusText)
	if err != nil || (status != 0 && status != 1) {
		writeResult(w, http.StatusInternalServerError, nil, "停止参数异常")
		return
	}
	if status == 0 {
		writeResult(w, http.StatusOK, nil, "正在重启")
	} else {
		writeResult(w, http.StatusOK, nil, "正在关闭")
	}
	if a.shutdown != nil {
		restart := status == 0
		go func() {
			time.Sleep(3 * time.Second)
			a.shutdown(restart)
		}()
	}
}

func (a *App) webuiUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if isRequestTooLarge(err) {
			writeResult(w, http.StatusRequestEntityTooLarge, nil, "WebUI 文件过大")
		} else {
			writeResult(w, http.StatusInternalServerError, nil, "Content-Type is not supported")
		}
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil || header == nil || strings.ToLower(filepath.Ext(header.Filename)) != ".zip" {
		writeResult(w, http.StatusInternalServerError, nil, "文件格式错误")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil || len(data) > maxUploadBytes {
		writeResult(w, http.StatusRequestEntityTooLarge, nil, "WebUI 文件过大")
		return
	}
	if err := a.installWebUI(data); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "WebUI 上传完成, 请刷新页面")
}
func (a *App) installWebUI(data []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("上传 WebUI 失败")
	}
	valid := false
	for _, entry := range reader.File {
		name, ok := safeArchiveName(entry.Name)
		if !ok || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return errors.New("上传 WebUI 失败")
		}
		if name == "webui.json" {
			valid = true
		}
	}
	if !valid {
		return errors.New("上传 WebUI 失败")
	}
	temp, err := os.MkdirTemp(a.configDir, ".webui-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := extractArchive(data, temp, maxZipExpandedBytes); err != nil {
		return errors.New("上传 WebUI 失败")
	}
	target := filepath.Join(a.configDir, "webui")
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return os.Rename(temp, target)
}
func extractArchive(data []byte, target string, maxBytes int64) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range reader.File {
		name, ok := safeArchiveName(entry.Name)
		if !ok || entry.UncompressedSize64 > maxZipEntryBytes {
			return errors.New("archive entry is unsafe")
		}
		total += int64(entry.UncompressedSize64)
		if total > maxBytes {
			return errors.New("archive is too large")
		}
		pathName := filepath.Join(target, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(pathName, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(pathName), 0o755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		content, readErr := io.ReadAll(io.LimitReader(input, maxZipEntryBytes+1))
		_ = input.Close()
		if readErr != nil || len(content) > maxZipEntryBytes {
			return errors.New("archive entry is too large")
		}
		if err := os.WriteFile(pathName, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) webuiDelete(w http.ResponseWriter, _ *http.Request) {
	if err := os.RemoveAll(filepath.Join(a.configDir, "webui")); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	writeResult(w, http.StatusOK, nil, "WebUI 删除完成")
}
func (a *App) webuiGetUpdate(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusInternalServerError, nil, "无 WebUI 更新")
}
func (a *App) webuiUpdate(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, http.StatusInternalServerError, nil, "无 WebUI 更新")
}

func allBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func isRequestTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

func formatBytes(size int64) string {
	value := float64(size)
	suffix := "B"
	for _, next := range []string{"KiB", "MiB", "GiB", "TiB"} {
		if value < 1024 {
			break
		}
		value /= 1024
		suffix = next
	}
	return fmt.Sprintf("%.2f %s", value, suffix)
}
