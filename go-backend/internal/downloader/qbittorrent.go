// Package downloader contains protocol adapters. qBittorrent is kept behind
// this boundary so orchestration tests can use a fake HTTP server.
package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

type QBittorrent struct {
	Host, Username, Password, APIKey string
	ContentLayout                    string
	UseDownloadPath                  bool
	RatioLimit                       int64
	SeedingTimeLimit                 int64
	InactiveSeedingTimeLimit         int64
	UpLimit, DlLimit                 int64
	Rename                           string
	Client                           *http.Client
	mu                               sync.RWMutex
	sessionCookie                    string
}

func (q *QBittorrent) httpClient() *http.Client {
	if q.Client != nil {
		return q.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}
func (q *QBittorrent) endpoint(path string) string { return strings.TrimRight(q.Host, "/") + path }

func (q *QBittorrent) Login(ctx context.Context) error {
	if strings.TrimSpace(q.Host) == "" {
		return errors.New("qBittorrent 未配置完成")
	}
	if strings.TrimSpace(q.Username) != "" {
		if strings.TrimSpace(q.Password) == "" {
			return errors.New("qBittorrent 未配置完成")
		}
		form := url.Values{"username": []string{q.Username}, "password": []string{q.Password}}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint("/api/v2/auth/login"), strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := q.httpClient().Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if response.StatusCode < 200 || response.StatusCode >= 300 || !strings.HasPrefix(strings.TrimSpace(string(body)), "Ok") {
			return fmt.Errorf("qBittorrent login returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		for _, cookie := range response.Cookies() {
			if cookie.Name == "SID" {
				q.mu.Lock()
				q.sessionCookie = cookie.Name + "=" + cookie.Value
				q.mu.Unlock()
			}
		}
		return nil
	}
	if strings.TrimSpace(q.APIKey) == "" {
		return errors.New("qBittorrent 未配置完成")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, q.endpoint("/api/v2/app/version"), nil)
	if err != nil {
		return err
	}
	q.authorize(request)
	response, err := q.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("qBittorrent login returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (q *QBittorrent) Add(ctx context.Context, resource model.Resource, savePath string, tags []string, paused bool) error {
	contentLayout := q.ContentLayout
	if contentLayout == "" {
		contentLayout = "Original"
	}
	form := url.Values{
		"addToTopOfQueue":          []string{"false"},
		"autoTMM":                  []string{"false"},
		"category":                 []string{"ani-rss"},
		"contentLayout":            []string{contentLayout},
		"dlLimit":                  []string{strconv.FormatInt(q.DlLimit, 10)},
		"firstLastPiecePrio":       []string{"false"},
		"savepath":                 []string{savePath},
		"sequentialDownload":       []string{"false"},
		"skip_checking":            []string{"false"},
		"stopCondition":            []string{"None"},
		"upLimit":                  []string{strconv.FormatInt(q.UpLimit, 10)},
		"useDownloadPath":          []string{strconv.FormatBool(q.UseDownloadPath)},
		"tags":                     []string{strings.Join(tags, ",")},
		"ratioLimit":               []string{strconv.FormatInt(q.RatioLimit, 10)},
		"seedingTimeLimit":         []string{strconv.FormatInt(q.SeedingTimeLimit, 10)},
		"inactiveSeedingTimeLimit": []string{strconv.FormatInt(q.InactiveSeedingTimeLimit, 10)},
		"paused":                   []string{strconv.FormatBool(paused)},
		"stopped":                  []string{strconv.FormatBool(paused)},
	}
	if q.Rename != "" {
		form.Set("rename", q.Rename)
	}
	if resource.Magnet != "" {
		form.Set("urls", resource.Magnet)
	} else {
		form.Set("urls", resource.DownloadURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint("/api/v2/torrents/add"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	q.authorize(request)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := q.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 || (len(body) > 0 && strings.TrimSpace(string(body)) != "Ok") {
		return fmt.Errorf("qBittorrent add returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (q *QBittorrent) Torrents(ctx context.Context) ([]model.Torrent, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, q.endpoint("/api/v2/torrents/info"), nil)
	if err != nil {
		return nil, err
	}
	q.authorize(request)
	response, err := q.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("qBittorrent info returned HTTP %d", response.StatusCode)
	}
	var raw []qbTorrent
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	result := make([]model.Torrent, 0, len(raw))
	for _, item := range raw {
		downloaded := item.Size - item.AmountLeft
		if downloaded < 0 {
			downloaded = 0
		}
		progress := item.Progress
		if progress > 0 && progress <= 1 {
			progress *= 100
		}
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		tags := []string{}
		for _, tag := range strings.Split(item.Tags, ",") {
			if strings.TrimSpace(tag) != "" {
				tags = append(tags, strings.TrimSpace(tag))
			}
		}
		state := mapState(item.State, progress)
		// Match Java's public contract: the global torrent page only exposes
		// tasks created by ANI-RSS, either by category or by tag.
		if item.Category != "ani-rss" && !containsTag(tags, "ani-rss") {
			continue
		}
		result = append(result, model.Torrent{ID: item.Hash, Hash: item.Hash, Name: item.Name, State: state, Progress: progress, Size: item.Size, Downloaded: downloaded, Completed: downloaded, FormatSize: formatSize(item.Size), SavePath: item.SavePath, Category: item.Category, Tags: tags, TagList: tags, AddedOn: item.AddedOn, CompletedOn: item.CompletionOn})
	}
	return result, nil
}

func containsTag(tags []string, target string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), target) {
			return true
		}
	}
	return false
}

// mapState turns qBittorrent's protocol states into the stable values exposed
// by the Java UI. In particular every successful terminal/upload state is
// represented as stoppedUP, which is the UI's completed bucket.
func mapState(state string, progress float64) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "error":
		return "error"
	case "missingfiles":
		return "missingFiles"
	case "pauseddl", "stoppeddl":
		return "stoppedDL"
	case "pausedup", "stoppedup":
		return "stoppedUP"
	case "forceddl":
		return "forcedDL"
	case "downloading":
		return "downloading"
	case "forcedmetadl":
		return "forcedMetaDL"
	case "metadl":
		return "metaDL"
	case "stalleddl":
		return "stalledDL"
	case "forcedup":
		return "forcedUP"
	case "uploading":
		return "uploading"
	case "stalledup":
		return "stalledUP"
	case "checkingresumedata":
		return "checkingResumeData"
	case "queueddl":
		return "queuedDL"
	case "queuedup":
		return "queuedUP"
	case "checkingup":
		return "checkingUP"
	case "checkingdl":
		return "checkingDL"
	case "moving":
		return "moving"
	case "allocating":
		return "allocating"
	default:
		if progress >= 100 {
			return "stoppedUP"
		}
		return "unknown"
	}
}

func formatSize(size int64) string {
	value, suffix := float64(size), "B"
	for _, next := range []string{"KiB", "MiB", "GiB", "TiB"} {
		if value < 1024 {
			break
		}
		value /= 1024
		suffix = next
	}
	return fmt.Sprintf("%.2f %s", value, suffix)
}

type qbTorrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
	Size         int64   `json:"size"`
	AmountLeft   int64   `json:"amount_left"`
	SavePath     string  `json:"save_path"`
	Category     string  `json:"category"`
	Tags         string  `json:"tags"`
	AddedOn      int64   `json:"added_on"`
	CompletionOn int64   `json:"completion_on"`
}

func (q *QBittorrent) Delete(ctx context.Context, hash string, deleteFiles bool) error {
	form := url.Values{"hashes": []string{hash}, "deleteFiles": []string{strconv.FormatBool(deleteFiles)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint("/api/v2/torrents/delete"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	q.authorize(request)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := q.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("qBittorrent delete returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (q *QBittorrent) RenameFile(ctx context.Context, hash, oldPath, newPath string) error {
	form := url.Values{"hash": []string{hash}, "oldPath": []string{oldPath}, "newPath": []string{newPath}}
	return q.postForm(ctx, "/api/v2/torrents/renameFile", form)
}

func (q *QBittorrent) AddTags(ctx context.Context, hash, tags string) error {
	return q.postForm(ctx, "/api/v2/torrents/addTags", url.Values{"hashes": []string{hash}, "tags": []string{tags}})
}
func (q *QBittorrent) SetSavePath(ctx context.Context, hash, path string) error {
	return q.postForm(ctx, "/api/v2/torrents/setSavePath", url.Values{"id": []string{hash}, "path": []string{path}})
}
func (q *QBittorrent) Start(ctx context.Context, hash string) error {
	return q.postForm(ctx, "/api/v2/torrents/start", url.Values{"hashes": []string{hash}})
}
func (q *QBittorrent) postForm(ctx context.Context, path string, form url.Values) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint(path), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	q.authorize(request)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := q.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("qBittorrent %s returned HTTP %d", path, response.StatusCode)
	}
	return nil
}

func (q *QBittorrent) authorize(request *http.Request) {
	if q.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+q.APIKey)
	}
	q.mu.RLock()
	cookie := q.sessionCookie
	q.mu.RUnlock()
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
}

// AddMultipart is retained for torrent-file sources; magnet/remote URL adds
// use Add and therefore share idempotency and request semantics.
func (q *QBittorrent) AddMultipart(ctx context.Context, fields map[string]string, name string, content []byte) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("torrents", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint("/api/v2/torrents/add"), &body)
	if err != nil {
		return err
	}
	q.authorize(request)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := q.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("qBittorrent multipart add returned HTTP %d", response.StatusCode)
	}
	return nil
}
