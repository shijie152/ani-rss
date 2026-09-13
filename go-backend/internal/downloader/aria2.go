package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// Aria2 implements aria2's JSON-RPC endpoint. The configured password is the
// RPC secret and is sent as the first token parameter on every call.
type Aria2 struct {
	Host, Token string
	Client      *http.Client
}

type ariaRPC struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type ariaTorrent struct {
	GID             string          `json:"gid"`
	TotalLength     string          `json:"totalLength"`
	CompletedLength string          `json:"completedLength"`
	Status          string          `json:"status"`
	Files           []ariaFile      `json:"files"`
	Bittorrent      *ariaBittorrent `json:"bittorrent"`
	InfoHash        string          `json:"infoHash"`
	Dir             string          `json:"dir"`
}
type ariaBittorrent struct {
	Info *struct {
		Name string `json:"name"`
	} `json:"info"`
}
type ariaFile struct {
	Path string `json:"path"`
}

func (a *Aria2) Login(ctx context.Context) error {
	if strings.TrimSpace(a.Host) == "" || strings.TrimSpace(a.Token) == "" {
		return errors.New("Aria2 未配置完成")
	}
	_, err := a.rpc(ctx, "aria2.getGlobalStat", nil)
	if err != nil {
		return fmt.Errorf("Aria2 登录失败: %w", err)
	}
	return nil
}

func (a *Aria2) rpc(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if strings.TrimSpace(a.Host) == "" {
		return nil, errors.New("Aria2 地址不能为空")
	}
	arguments := []any{"token:" + a.Token}
	arguments = append(arguments, params...)
	payload := ariaRPC{JSONRPC: "2.0", ID: "ani-rss", Method: method, Params: arguments}
	data, _, err := jsonRequest(ctx, a.Client, endpoint(a.Host, "/jsonrpc"), nil, payload)
	if err != nil {
		return nil, fmt.Errorf("Aria2 %s: %w", method, err)
	}
	if rpcErr := rpcError(data); rpcErr != nil {
		return nil, fmt.Errorf("Aria2 %s: %w", method, rpcErr)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("Aria2 %s 响应异常: %w", method, err)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil, fmt.Errorf("Aria2 %s 缺少 result", method)
	}
	return envelope.Result, nil
}

func (a *Aria2) Add(ctx context.Context, resource model.Resource, savePath string, _ []string, _ bool) error {
	uri := strings.TrimSpace(resource.Magnet)
	if uri != "" {
		_, err := a.rpc(ctx, "aria2.addUri", []any{[]string{uri}, map[string]any{"dir": savePath}})
		return err
	}
	data, err := torrentBytes(ctx, a.Client, resource)
	if err != nil {
		return err
	}
	_, err = a.rpc(ctx, "aria2.addTorrent", []any{encodedTorrent(data), []any{}, map[string]any{"dir": savePath}})
	return err
}

func (a *Aria2) Torrents(ctx context.Context) ([]model.Torrent, error) {
	result := make([]model.Torrent, 0)
	for _, call := range []struct {
		method string
		params []any
	}{
		{"aria2.tellActive", []any{[]string{"gid", "totalLength", "completedLength", "status", "files", "bittorrent", "infoHash", "dir"}}},
		{"aria2.tellWaiting", []any{-1, 1000, []string{"gid", "totalLength", "completedLength", "status", "files", "bittorrent", "infoHash", "dir"}}},
		{"aria2.tellStopped", []any{-1, 1000, []string{"gid", "totalLength", "completedLength", "status", "files", "bittorrent", "infoHash", "dir"}}},
	} {
		data, err := a.rpc(ctx, call.method, call.params)
		if err != nil {
			return nil, err
		}
		var tasks []ariaTorrent
		if err := json.Unmarshal(data, &tasks); err != nil {
			return nil, fmt.Errorf("Aria2 %s 结果异常: %w", call.method, err)
		}
		for _, raw := range tasks {
			if raw.Bittorrent == nil || raw.Bittorrent.Info == nil || strings.TrimSpace(raw.Bittorrent.Info.Name) == "" {
				continue
			}
			result = append(result, mapAriaTorrent(raw))
		}
	}
	return result, nil
}

func mapAriaTorrent(raw ariaTorrent) model.Torrent {
	total := parseInt64(raw.TotalLength)
	completed := parseInt64(raw.CompletedLength)
	progress := 0.0
	if total > 0 {
		progress = float64(completed) / float64(total) * 100
	}
	state := "downloading"
	switch strings.ToLower(raw.Status) {
	case "complete":
		state = "stoppedUP"
	case "waiting":
		state = "queuedDL"
	case "paused":
		if progress >= 100 {
			state = "stoppedUP"
		} else {
			state = "stoppedDL"
		}
	case "error", "removed":
		state = "error"
	case "checking":
		state = "checkingDL"
	}
	name := raw.Bittorrent.Info.Name
	return model.Torrent{ID: raw.GID, Hash: raw.InfoHash, Name: name, State: state, Progress: progress, Size: total, Downloaded: completed, Completed: completed, FormatSize: formatSize(total), SavePath: raw.Dir, Category: "ani-rss", TagList: []string{}, Tags: []string{}}
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func (a *Aria2) WaitForCompletion(ctx context.Context, id string, interval time.Duration) (model.Torrent, error) {
	if strings.TrimSpace(id) == "" {
		return model.Torrent{}, errors.New("Aria2 gid/hash 不能为空")
	}
	for {
		tasks, err := a.Torrents(ctx)
		if err != nil {
			return model.Torrent{}, err
		}
		if task, ok := findTorrent(tasks, id); ok {
			switch task.State {
			case "stoppedUP":
				return task, nil
			case "error", "missingFiles":
				return task, fmt.Errorf("Aria2 任务 %s 完成失败: %s", id, task.State)
			}
		}
		if err := waitContext(ctx, interval); err != nil {
			return model.Torrent{}, err
		}
	}
}

func (a *Aria2) Delete(ctx context.Context, id string, _ bool) error {
	_, err := a.rpc(ctx, "aria2.removeDownloadResult", []any{id})
	return err
}

func (a *Aria2) RenameFile(_ context.Context, _, _, _ string) error { return errUnsupported }

func (a *Aria2) AddTags(_ context.Context, _, _ string) error { return errUnsupported }

func (a *Aria2) SetSavePath(ctx context.Context, id, path string) error {
	_, err := a.rpc(ctx, "aria2.changeOption", []any{id, map[string]string{"dir": path}})
	return err
}

func (a *Aria2) Start(ctx context.Context, id string) error {
	_, err := a.rpc(ctx, "aria2.unpause", []any{id})
	return err
}

func (a *Aria2) UpdateTrackers(ctx context.Context, trackers []string) error {
	_, err := a.rpc(ctx, "aria2.changeGlobalOption", []any{map[string]string{"bt-tracker": strings.Join(trackers, ", ")}})
	return err
}

var _ Adapter = (*Aria2)(nil)
