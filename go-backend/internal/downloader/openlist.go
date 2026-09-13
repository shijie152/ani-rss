package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// OpenList is the remote/offline downloader adapter. Unlike torrent clients,
// an offline task is not complete when the add request returns, so Add waits
// for the task's terminal state and retries failed tasks within the configured
// timeout.
type OpenList struct {
	Host, Token, Provider string
	Client                *http.Client
	Timeout               time.Duration
	Retries               int
	PollInterval          time.Duration
}

type openListResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type openListTask struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    any    `json:"state"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Error    string `json:"error"`
	Total    string `json:"totalBytes"`
}

type openListFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Dir  bool   `json:"is_dir"`
}

func (o *OpenList) Login(ctx context.Context) error {
	if strings.TrimSpace(o.Host) == "" || strings.TrimSpace(o.Token) == "" {
		return errors.New("OpenList 未配置完成")
	}
	if strings.TrimSpace(o.Provider) == "" {
		return errors.New("OpenList Driver 未配置")
	}
	response, err := o.call(ctx, http.MethodPost, "me", nil, nil)
	if err != nil {
		return fmt.Errorf("OpenList 登录失败: %w", err)
	}
	if response.Code != 0 && response.Code != http.StatusOK {
		return fmt.Errorf("OpenList 登录失败: %s", response.Message)
	}
	return nil
}

func (o *OpenList) call(ctx context.Context, method, action string, payload any, form url.Values) (openListResponse, error) {
	headers := http.Header{"Authorization": []string{o.Token}}
	var body []byte
	var err error
	if form != nil {
		body = []byte(form.Encode())
		headers.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return openListResponse{}, err
		}
		headers.Set("Content-Type", "application/json")
	}
	data, _, _, err := request(ctx, o.Client, method, endpoint(o.Host, "/api/"+strings.TrimLeft(action, "/")), headers, body)
	if err != nil {
		return openListResponse{}, err
	}
	var response openListResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return openListResponse{}, fmt.Errorf("OpenList %s 响应异常: %w", action, err)
	}
	if response.Code != 0 && response.Code != http.StatusOK {
		return response, fmt.Errorf("OpenList %s: %s (code %d)", action, response.Message, response.Code)
	}
	return response, nil
}

func (o *OpenList) Add(ctx context.Context, resource model.Resource, savePath string, _ []string, _ bool) error {
	uri := strings.TrimSpace(resource.Magnet)
	if uri == "" {
		uri = strings.TrimSpace(resource.TorrentURL)
	}
	if uri == "" {
		uri = strings.TrimSpace(resource.DownloadURL)
	}
	if uri == "" {
		return errors.New("OpenList 资源地址不能为空")
	}
	if strings.TrimSpace(o.Provider) == "" {
		return errors.New("OpenList Driver 未配置")
	}
	response, err := o.call(ctx, http.MethodPost, "fs/add_offline_download", map[string]any{
		"path": savePath, "urls": []string{uri}, "tool": o.Provider, "delete_policy": "delete_on_upload_succeed",
	}, nil)
	if err != nil {
		return err
	}
	id, err := offlineTaskID(response.Data)
	if err != nil {
		return err
	}
	_, err = o.WaitForCompletion(ctx, id, o.PollInterval)
	return err
}

func offlineTaskID(data json.RawMessage) (string, error) {
	var value struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("OpenList 任务响应异常: %w", err)
	}
	if len(value.Tasks) == 0 || strings.TrimSpace(value.Tasks[0].ID) == "" {
		return "", errors.New("OpenList 未返回离线任务 ID")
	}
	return value.Tasks[0].ID, nil
}

func (o *OpenList) task(ctx context.Context, id string) (openListTask, error) {
	response, err := o.call(ctx, http.MethodPost, "task/offline_download/info?tid="+url.QueryEscape(id), nil, nil)
	if err != nil {
		return openListTask{}, err
	}
	var task openListTask
	if err := json.Unmarshal(response.Data, &task); err != nil {
		return openListTask{}, fmt.Errorf("OpenList 任务 %s 响应异常: %w", id, err)
	}
	return task, nil
}

func (o *OpenList) WaitForCompletion(ctx context.Context, id string, interval time.Duration) (model.Torrent, error) {
	if strings.TrimSpace(id) == "" {
		return model.Torrent{}, errors.New("OpenList task id 不能为空")
	}
	deadline := time.Time{}
	if o.Timeout > 0 {
		deadline = time.Now().Add(o.Timeout)
	}
	retries := 0
	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return model.Torrent{}, fmt.Errorf("OpenList 任务 %s 超时", id)
		}
		task, err := o.task(ctx, id)
		if err != nil {
			if waitErr := waitContext(ctx, interval); waitErr != nil {
				return model.Torrent{}, waitErr
			}
			continue
		}
		mapped := mapOpenListTask(task)
		switch mapped.State {
		case "stoppedUP":
			return mapped, nil
		case "stoppedDL":
			return mapped, fmt.Errorf("OpenList 任务 %s 已取消", id)
		case "error":
			if o.Retries >= 0 && retries >= o.Retries {
				return mapped, fmt.Errorf("OpenList 任务 %s 失败: %s", id, task.Error)
			}
			retries++
			if err := o.retry(ctx, id); err != nil {
				return mapped, err
			}
		}
		if err := waitContext(ctx, interval); err != nil {
			return model.Torrent{}, err
		}
	}
}

func openListState(value any, fallback string) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.Itoa(int(typed))
	case int:
		return strconv.Itoa(typed)
	default:
		return fallback
	}
}

func mapOpenListTask(task openListTask) model.Torrent {
	state := strings.ToLower(openListState(task.State, task.Status))
	switch state {
	case "succeeded", "success", "done", "complete", "completed":
		state = "stoppedUP"
	case "pending", "running", "waiting", "preparing", "preparing_to_retry", "waiting_for_retry":
		state = "downloading"
	case "canceling":
		state = "stoppedDL"
	case "canceled", "error", "failing", "failed":
		state = "error"
	default:
		if code, err := strconv.Atoi(state); err == nil {
			switch code {
			case 0:
				state = "queuedDL"
			case 1:
				state = "downloading"
			case 2:
				state = "stoppedUP"
			case 3:
				state = "stoppedDL"
			case 4, 5, 6, 7:
				state = "error"
			default:
				state = "unknown"
			}
		}
	}
	return model.Torrent{ID: task.ID, Hash: task.ID, Name: task.Name, State: state, Progress: float64(task.Progress), Size: parseInt64(task.Total), Completed: int64(float64(task.Progress)), FormatSize: formatSize(parseInt64(task.Total)), Category: "ani-rss"}
}

func (o *OpenList) Torrents(ctx context.Context) ([]model.Torrent, error) {
	result := make([]model.Torrent, 0)
	for _, action := range []string{"task/offline_download/undone", "task/offline_download/done"} {
		response, err := o.call(ctx, http.MethodGet, action, nil, nil)
		if err != nil {
			return nil, err
		}
		var tasks []openListTask
		if err := json.Unmarshal(response.Data, &tasks); err != nil {
			return nil, fmt.Errorf("OpenList %s 结果异常: %w", action, err)
		}
		for _, task := range tasks {
			result = append(result, mapOpenListTask(task))
		}
	}
	return result, nil
}

func (o *OpenList) retry(ctx context.Context, id string) error {
	_, err := o.call(ctx, http.MethodPost, "task/offline_download/retry", nil, url.Values{"tid": []string{id}})
	return err
}

func (o *OpenList) Delete(ctx context.Context, id string, _ bool) error {
	_, err := o.call(ctx, http.MethodPost, "task/offline_download/delete_some", []string{id}, nil)
	return err
}

func (o *OpenList) RenameFile(ctx context.Context, _, oldPath, newPath string) error {
	directory := path.Dir(oldPath)
	if directory == "." {
		directory = "/"
	}
	_, err := o.call(ctx, http.MethodPost, "fs/batch_rename", map[string]any{"src_dir": directory, "rename_objects": []map[string]string{{"src_name": path.Base(oldPath), "new_name": path.Base(newPath)}}}, nil)
	return err
}

func (o *OpenList) AddTags(_ context.Context, _, _ string) error { return errUnsupported }

func (o *OpenList) SetSavePath(_ context.Context, _, _ string) error { return errUnsupported }

func (o *OpenList) Start(_ context.Context, _ string) error { return errUnsupported }

func (o *OpenList) UpdateTrackers(_ context.Context, _ []string) error { return errUnsupported }

// Move performs the remote filesystem operation used after an offline task
// has been verified. It remains separate from SetSavePath because OpenList
// moves files, not torrent-client tasks.
func (o *OpenList) Move(ctx context.Context, source, destination string, names []string) error {
	_, err := o.call(ctx, http.MethodPost, "fs/move", map[string]any{"src_dir": source, "dst_dir": destination, "names": names}, nil)
	return err
}

func (o *OpenList) Mkdir(ctx context.Context, directory string) error {
	_, err := o.call(ctx, http.MethodPost, "fs/mkdir", map[string]string{"path": directory}, nil)
	return err
}

func (o *OpenList) Remove(ctx context.Context, directory string, names []string) error {
	_, err := o.call(ctx, http.MethodPost, "fs/remove", map[string]any{"dir": directory, "names": names}, nil)
	return err
}

// FindFiles recursively lists remote files. It is exported for media and
// collection workflows that need to verify an offline task before moving it.
func (o *OpenList) FindFiles(ctx context.Context, directory string) ([]string, error) {
	response, err := o.call(ctx, http.MethodPost, "fs/list", map[string]any{"path": directory, "page": 1, "per_page": 0, "refresh": true}, nil)
	if err != nil {
		return nil, err
	}
	var data struct {
		Content []openListFile `json:"content"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, item := range data.Content {
		child := path.Join(directory, item.Name)
		if item.Dir {
			nested, nestedErr := o.FindFiles(ctx, child)
			if nestedErr != nil {
				return nil, nestedErr
			}
			files = append(files, nested...)
		} else {
			files = append(files, child)
		}
	}
	return files, nil
}

var _ Adapter = (*OpenList)(nil)
