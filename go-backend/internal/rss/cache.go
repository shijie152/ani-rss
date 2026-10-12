package rss

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/outbound"
)

func resourceCacheDir(configDir string, ani model.Ani) string {
	return filepath.Join(configDir, "torrents", safeHash(ani.ID))
}

func HasCachedResource(configDir string, ani model.Ani, resource model.Resource) bool {
	if strings.TrimSpace(resource.InfoHash) == "" {
		return false
	}
	for _, ext := range []string{".txt", ".torrent"} {
		if info, err := os.Stat(filepath.Join(resourceCacheDir(configDir, ani), safeHash(resource.InfoHash)+ext)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func SaveResourceCache(ctx context.Context, client *outbound.Client, configDir string, ani model.Ani, resource model.Resource) error {
	if strings.TrimSpace(resource.InfoHash) == "" {
		return errors.New("资源 hash 为空")
	}
	if HasCachedResource(configDir, ani, resource) {
		return nil
	}
	address := strings.TrimSpace(resource.TorrentURL)
	if address == "" {
		address = strings.TrimSpace(resource.DownloadURL)
	}
	if address == "" {
		return errors.New("资源地址为空")
	}
	data := []byte(address)
	ext := ".txt"
	if strings.HasPrefix(strings.ToLower(address), "http://") || strings.HasPrefix(strings.ToLower(address), "https://") {
		ext = ".torrent"
		if client == nil {
			return errors.New("资源客户端未配置")
		}
		downloaded, err := client.Read(ctx, outbound.Call{
			Service:      "resource",
			Method:       http.MethodGet,
			URL:          address,
			MaxBytes:     64 << 20,
			StatusPrefix: "资源服务返回 ",
		})
		if err != nil {
			return err
		}
		data = downloaded
	}
	if len(data) == 0 {
		return errors.New("资源内容为空")
	}
	target := filepath.Join(resourceCacheDir(configDir, ani), safeHash(resource.InfoHash)+ext)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".cache-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, target); err != nil {
		return err
	}
	ok = true
	return nil
}

func DeleteResourceCache(configDir string, ani model.Ani, hashes map[string]bool) error {
	directory := resourceCacheDir(configDir, ani)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if hashes[strings.ToLower(base)] {
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeHash(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "invalid-" + fmt.Sprintf("%x", []byte(value))
		}
	}
	return value
}
