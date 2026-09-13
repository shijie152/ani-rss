package downloader

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// Adapter is the stable downloader boundary used by RSS and HTTP handlers.
// Protocol-specific details stay behind this interface so changing the
// configured downloader does not change the UI or matching rules.
type Adapter interface {
	Login(context.Context) error
	Add(context.Context, model.Resource, string, []string, bool) error
	Torrents(context.Context) ([]model.Torrent, error)
	WaitForCompletion(context.Context, string, time.Duration) (model.Torrent, error)
	Delete(context.Context, string, bool) error
	RenameFile(context.Context, string, string, string) error
	AddTags(context.Context, string, string) error
	SetSavePath(context.Context, string, string) error
	Start(context.Context, string) error
	UpdateTrackers(context.Context, []string) error
}

// New returns the adapter selected by the same downloadToolType value used by
// the existing Vue configuration page.
func New(cfg model.Config, client *http.Client) (Adapter, error) {
	typeName := strings.ToLower(strings.TrimSpace(appconfig.String(cfg, "downloadToolType")))
	switch typeName {
	case "", "qbittorrent":
		username := appconfig.String(cfg, "downloadToolUsername")
		password := appconfig.String(cfg, "downloadToolPassword")
		apiKey := ""
		if username == "" {
			apiKey = password
		}
		return &QBittorrent{
			Host: appconfig.String(cfg, "downloadToolHost"), Username: username, Password: password,
			APIKey: apiKey, Client: client, ContentLayout: appconfig.String(cfg, "qbContentLayout"),
			UseDownloadPath:          appconfig.Bool(cfg, "qbUseDownloadPath"),
			RatioLimit:               int64(appconfig.Int(cfg, "ratioLimit")),
			SeedingTimeLimit:         int64(appconfig.Int(cfg, "seedingTimeLimit")),
			InactiveSeedingTimeLimit: int64(appconfig.Int(cfg, "inactiveSeedingTimeLimit")),
			UpLimit:                  int64(appconfig.Int(cfg, "upLimit")) * 1024,
			DlLimit:                  int64(appconfig.Int(cfg, "dlLimit")) * 1024,
		}, nil
	case "transmission":
		return &Transmission{Host: appconfig.String(cfg, "downloadToolHost"), Username: appconfig.String(cfg, "downloadToolUsername"), Password: appconfig.String(cfg, "downloadToolPassword"), Client: client}, nil
	case "aria2":
		return &Aria2{Host: appconfig.String(cfg, "downloadToolHost"), Token: appconfig.String(cfg, "downloadToolPassword"), Client: client}, nil
	case "openlist":
		return &OpenList{
			Host: appconfig.String(cfg, "downloadToolHost"), Token: appconfig.String(cfg, "downloadToolPassword"),
			Provider: appconfig.String(cfg, "provider"), Client: client,
			Timeout:      time.Duration(appconfig.Int(cfg, "openListDownloadTimeout")) * time.Minute,
			Retries:      appconfig.Int(cfg, "openListDownloadRetryNumber"),
			PollInterval: 2 * time.Second,
		}, nil
	default:
		return nil, fmt.Errorf("不支持的下载器: %s", appconfig.String(cfg, "downloadToolType"))
	}
}

func clientOrDefault(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// request performs bounded retries for transport failures and 5xx responses.
// Request bodies are retained as bytes, so a retry cannot accidentally send an
// empty JSON-RPC body after the first attempt.
func request(ctx context.Context, client *http.Client, method, endpoint string, headers http.Header, body []byte) ([]byte, int, http.Header, error) {
	client = clientOrDefault(client)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, 0, nil, err
		}
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		response, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<20))
			_ = response.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if response.StatusCode >= 500 && attempt < 2 {
				lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				return data, response.StatusCode, response.Header, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
			} else {
				return data, response.StatusCode, response.Header, nil
			}
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, 0, nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
			}
		}
	}
	return nil, 0, nil, lastErr
}

func jsonRequest(ctx context.Context, client *http.Client, endpoint string, headers http.Header, payload any) ([]byte, http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", "application/json")
	data, _, responseHeaders, err := request(ctx, client, http.MethodPost, endpoint, headers, body)
	return data, responseHeaders, err
}

func rpcError(data []byte) error {
	var envelope struct {
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Error != nil {
		return fmt.Errorf("RPC error %v: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return nil
}

func endpoint(host, suffix string) string {
	return strings.TrimRight(strings.TrimSpace(host), "/") + suffix
}

func basicAuth(username, password string) string {
	request, _ := http.NewRequest(http.MethodGet, "http://invalid", nil)
	request.SetBasicAuth(username, password)
	return request.Header.Get("Authorization")
}

func findTorrent(tasks []model.Torrent, id string) (model.Torrent, bool) {
	for _, task := range tasks {
		if strings.EqualFold(task.ID, id) || strings.EqualFold(task.Hash, id) {
			return task, true
		}
	}
	return model.Torrent{}, false
}

func waitContext(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errUnsupported = errors.New("当前下载器不支持此操作")

// torrentBytes resolves a remote RSS enclosure into its metainfo bytes. RSS
// resources use Magnet for magnets and TorrentURL/DownloadURL for torrent
// files; the latter must be sent as metainfo to RPC clients rather than as a
// normal HTTP download URI.
func torrentBytes(ctx context.Context, client *http.Client, resource model.Resource) ([]byte, error) {
	address := strings.TrimSpace(resource.TorrentURL)
	if address == "" {
		address = strings.TrimSpace(resource.DownloadURL)
	}
	if address == "" {
		return nil, errors.New("种子地址不能为空")
	}
	data, _, _, err := request(ctx, client, http.MethodGet, address, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("获取种子文件失败: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("种子文件为空")
	}
	return data, nil
}

func encodedTorrent(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
