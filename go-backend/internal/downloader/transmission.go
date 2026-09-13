package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// Transmission implements the Transmission RPC protocol, including its
// mandatory 409/session-id challenge.
type Transmission struct {
	Host, Username, Password string
	Client                   *http.Client
	mu                       sync.RWMutex
	sessionID                string
}

type transmissionRPC struct {
	ID      string         `json:"id"`
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type transmissionEnvelope struct {
	Result *struct {
		Torrents []transmissionTorrent `json:"torrents"`
		Added    *struct {
			Hash string `json:"hashString"`
		} `json:"torrent-added"`
		Duplicate *struct {
			Hash string `json:"hashString"`
		} `json:"torrent-duplicate"`
	} `json:"arguments"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type transmissionTorrent struct {
	Name        string             `json:"name"`
	Labels      []string           `json:"labels"`
	Hash        string             `json:"hashString"`
	Files       []transmissionFile `json:"files"`
	Finished    bool               `json:"isFinished"`
	ID          int64              `json:"id"`
	DownloadDir string             `json:"downloadDir"`
	Status      int                `json:"status"`
	TotalSize   int64              `json:"totalSize"`
	HaveValid   int64              `json:"haveValid"`
}

type transmissionFile struct {
	Name string `json:"name"`
}

func (t *Transmission) Login(ctx context.Context) error {
	if strings.TrimSpace(t.Host) == "" || strings.TrimSpace(t.Username) == "" || strings.TrimSpace(t.Password) == "" {
		return errors.New("Transmission 未配置完成")
	}
	_, err := t.rpc(ctx, "torrent-get", map[string]any{"fields": []string{"id"}})
	if err != nil {
		return fmt.Errorf("Transmission 登录失败: %w", err)
	}
	return nil
}

func (t *Transmission) rpc(ctx context.Context, method string, params map[string]any) ([]byte, error) {
	if strings.TrimSpace(t.Host) == "" {
		return nil, errors.New("Transmission 地址不能为空")
	}
	body := transmissionRPC{ID: "ani-rss", JSONRPC: "2.0", Method: method, Params: params}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 4; attempt++ {
		headers := http.Header{}
		headers.Set("Authorization", basicAuth(t.Username, t.Password))
		t.mu.RLock()
		session := t.sessionID
		t.mu.RUnlock()
		if session != "" {
			headers.Set("X-Transmission-Session-Id", session)
		}
		data, status, responseHeaders, requestErr := request(ctx, t.Client, http.MethodPost, endpoint(t.Host, "/transmission/rpc"), headers, encoded)
		if status == http.StatusConflict {
			if next := responseHeaders.Get("X-Transmission-Session-Id"); next != "" {
				t.mu.Lock()
				t.sessionID = next
				t.mu.Unlock()
				continue
			}
		}
		if requestErr != nil {
			return nil, fmt.Errorf("Transmission %s: %w", method, requestErr)
		}
		if err := rpcError(data); err != nil {
			return nil, fmt.Errorf("Transmission %s: %w", method, err)
		}
		var envelope transmissionEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil, fmt.Errorf("Transmission %s 响应异常: %w", method, err)
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("Transmission %s: %s", method, envelope.Error.Message)
		}
		return data, nil
	}
	return nil, errors.New("Transmission session challenge exceeded retry limit")
}

func (t *Transmission) Add(ctx context.Context, resource model.Resource, savePath string, tags []string, paused bool) error {
	filename := strings.TrimSpace(resource.Magnet)
	metainfo := ""
	if filename == "" {
		data, err := torrentBytes(ctx, t.Client, resource)
		if err != nil {
			return err
		}
		metainfo = encodedTorrent(data)
	}
	_, err := t.rpc(ctx, "torrent-add", map[string]any{
		"labels": tags, "download_dir": savePath, "metainfo": metainfo, "filename": filename, "paused": paused,
	})
	if err != nil {
		return err
	}
	return nil
}

func (t *Transmission) Torrents(ctx context.Context) ([]model.Torrent, error) {
	data, err := t.rpc(ctx, "torrent-get", map[string]any{"fields": []string{"name", "labels", "hashString", "files", "isFinished", "id", "downloadDir", "status", "totalSize", "haveValid"}})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Arguments struct {
			Torrents []transmissionTorrent `json:"torrents"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	result := make([]model.Torrent, 0, len(envelope.Arguments.Torrents))
	for _, raw := range envelope.Arguments.Torrents {
		if !containsTag(raw.Labels, "ani-rss") {
			continue
		}
		progress := 0.0
		if raw.TotalSize > 0 {
			progress = float64(raw.HaveValid) / float64(raw.TotalSize) * 100
		}
		if progress > 100 {
			progress = 100
		}
		tags := append([]string(nil), raw.Labels...)
		result = append(result, model.Torrent{ID: strconv.FormatInt(raw.ID, 10), Hash: raw.Hash, Name: raw.Name, State: transmissionState(raw.Status, raw.Finished), Progress: progress, Size: raw.TotalSize, Downloaded: raw.HaveValid, Completed: raw.HaveValid, FormatSize: formatSize(raw.TotalSize), SavePath: raw.DownloadDir, Category: "ani-rss", Tags: tags, TagList: tags})
	}
	return result, nil
}

func transmissionState(status int, finished bool) string {
	if finished {
		return "stoppedUP"
	}
	switch status {
	case 1:
		return "checkingDL"
	case 2:
		return "checkingDL"
	case 3:
		return "queuedDL"
	case 4:
		return "downloading"
	case 5:
		return "queuedUP"
	case 6:
		return "stalledUP"
	default:
		return "unknown"
	}
}

func (t *Transmission) WaitForCompletion(ctx context.Context, hash string, interval time.Duration) (model.Torrent, error) {
	if strings.TrimSpace(hash) == "" {
		return model.Torrent{}, errors.New("种子 hash 不能为空")
	}
	for {
		tasks, err := t.Torrents(ctx)
		if err != nil {
			return model.Torrent{}, err
		}
		if task, ok := findTorrent(tasks, hash); ok {
			switch task.State {
			case "stoppedUP", "stalledUP", "queuedUP":
				return task, nil
			case "error", "missingFiles":
				return task, fmt.Errorf("种子 %s 完成失败: %s", hash, task.State)
			}
		}
		if err := waitContext(ctx, interval); err != nil {
			return model.Torrent{}, err
		}
	}
}

func (t *Transmission) Delete(ctx context.Context, hash string, deleteFiles bool) error {
	_, err := t.rpc(ctx, "torrent-remove", map[string]any{"ids": []string{hash}, "delete-local-data": deleteFiles})
	return err
}

func (t *Transmission) RenameFile(ctx context.Context, hash, oldPath, newPath string) error {
	_, err := t.rpc(ctx, "torrent-rename-path", map[string]any{"ids": []string{hash}, "path": oldPath, "name": newPath})
	return err
}

func (t *Transmission) AddTags(ctx context.Context, hash, tag string) error {
	tasks, err := t.Torrents(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if strings.EqualFold(task.Hash, hash) {
			tags := append([]string(nil), task.TagList...)
			if !containsTag(tags, tag) {
				tags = append(tags, tag)
			}
			_, err = t.rpc(ctx, "torrent-set", map[string]any{"ids": []string{hash}, "labels": tags})
			return err
		}
	}
	return fmt.Errorf("Transmission 任务不存在: %s", hash)
}

func (t *Transmission) SetSavePath(ctx context.Context, hash, path string) error {
	_, err := t.rpc(ctx, "torrent-set-location", map[string]any{"ids": []string{hash}, "location": path, "move": true})
	return err
}

func (t *Transmission) Start(ctx context.Context, hash string) error {
	_, err := t.rpc(ctx, "torrent-start", map[string]any{"ids": []string{hash}})
	return err
}

func (t *Transmission) UpdateTrackers(_ context.Context, _ []string) error { return errUnsupported }

var _ Adapter = (*Transmission)(nil)
