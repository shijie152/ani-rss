// Package collection implements the three collection endpoints used by the
// existing UI. Preview is pure; start adds the torrent paused, reconciles the
// qBittorrent file list, then starts the task.
package collection

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/media"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/regexutil"
	"github.com/shijie152/ani-rss/go-backend/internal/torrent"
)

type Service struct {
	Config   appconfig.Reader
	Download *downloader.QBittorrent
	Sleep    func(time.Duration)
}

func (s *Service) Preview(info model.CollectionInfo) ([]model.Item, error) {
	meta, err := parse(info.Torrent)
	if err != nil {
		return nil, err
	}
	items := make([]model.Item, 0, len(meta.Files))
	for _, member := range meta.Files {
		if !s.include(member.Path, info.Ani) {
			continue
		}
		episode, ok := media.EpisodeForName(member.Path, info.Ani)
		if !ok && !info.Ani.OVA {
			continue
		}
		if info.Ani.OVA {
			episode = 1
		}
		rename := media.CollectionFilename(info.Ani, member.Path, episode+float64(info.Ani.Offset), s.Config)
		if rename == "" {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(member.Path)), ".")
		if ext == "" {
			continue
		}
		if isSubtitle(member.Path) {
			language := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSuffix(filepath.Base(member.Path), filepath.Ext(member.Path)))), ".")
			if language != "" {
				rename += "." + language
			}
		}
		items = append(items, model.Item{Title: member.Path, ReName: rename + "." + ext, Episode: episode + float64(info.Ani.Offset), FormatSize: formatSize(member.Length), Length: member.Length, Subgroup: info.Ani.Subgroup})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Episode < items[j].Episode })
	return items, nil
}

var (
	collectionSubgroupPattern = regexp.MustCompile(`^\[(.+?)]`)
	collectionRulePattern     = regexp.MustCompile(`^\{\{(.+)}}:(.+)$`)
)

func (s *Service) Subgroup(info model.CollectionInfo) (string, error) {
	items, err := s.Preview(info)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		name := filepath.Base(filepath.ToSlash(item.Title))
		if match := collectionSubgroupPattern.FindStringSubmatch(name); len(match) > 1 {
			return match[1], nil
		}
	}
	return "未知字幕组", nil
}

func (s *Service) Start(ctx context.Context, info model.CollectionInfo) error {
	if s.Download == nil {
		return errors.New("合集下载暂时只支持 qBittorrent")
	}
	data, err := decodeTorrent(info.Torrent)
	if err != nil {
		return err
	}
	meta, err := torrent.Parse(data)
	if err != nil {
		return err
	}
	subgroup := info.Ani.Subgroup
	if subgroup == "" {
		subgroup, _ = s.Subgroup(info)
	}
	savePath := strings.TrimSpace(info.Ani.CustomDownloadPathTemplate)
	if savePath == "" {
		return errors.New("合集下载路径不能为空")
	}
	name := fmt.Sprintf("[%s] %s 第%d季", defaultValue(subgroup, "未知字幕组"), info.Ani.Title, info.Ani.Season)
	cfg := model.Config{}
	if s.Config != nil {
		cfg = s.Config.Snapshot()
	}
	fields := map[string]string{
		"addToTopOfQueue": "false", "autoTMM": "false", "category": "", "contentLayout": defaultConfigString(cfg, "qbContentLayout", "Original"),
		"dlLimit": fmt.Sprintf("%d", int64(appconfig.Int(cfg, "dlLimit"))*1024), "firstLastPiecePrio": "false", "paused": "true", "stopped": "true",
		"rename": name, "savepath": savePath, "sequentialDownload": "false", "skip_checking": "false", "stopCondition": "None",
		"upLimit": fmt.Sprintf("%d", int64(appconfig.Int(cfg, "upLimit"))*1024), "useDownloadPath": fmt.Sprintf("%t", appconfig.Bool(cfg, "qbUseDownloadPath")),
		"tags": "ANI-RSS合集下载," + defaultValue(subgroup, "未知字幕组"), "ratioLimit": fmt.Sprintf("%d", appconfig.Int(cfg, "ratioLimit")),
		"seedingTimeLimit": fmt.Sprintf("%d", appconfig.Int(cfg, "seedingTimeLimit")), "inactiveSeedingTimeLimit": fmt.Sprintf("%d", appconfig.Int(cfg, "inactiveSeedingTimeLimit")),
	}
	if err := s.Download.Login(ctx); err != nil {
		return err
	}
	if err := s.Download.AddMultipart(ctx, fields, "collection.torrent", data); err != nil {
		return err
	}
	files := []downloader.TorrentFile{}
	sleep := s.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for attempt := 0; attempt < 5; attempt++ {
		files, err = s.Download.Files(ctx, meta.InfoHash)
		if err == nil && len(files) > 0 {
			break
		}
		if err != nil && attempt == 4 {
			return err
		}
		sleep(500 * time.Millisecond)
	}
	preview, err := s.Preview(info)
	if err != nil {
		return err
	}
	rename := map[string]string{}
	for _, item := range preview {
		for _, file := range files {
			if filepath.Base(filepath.FromSlash(file.Name)) == filepath.Base(filepath.FromSlash(item.Title)) && file.Size == item.Length {
				rename[file.Name] = item.ReName
				break
			}
		}
	}
	for _, file := range files {
		newPath, wanted := rename[file.Name]
		if !wanted {
			if file.Priority > 0 {
				if err := s.Download.SetFilePriority(ctx, meta.InfoHash, file.Index, 0); err != nil {
					return err
				}
			}
			continue
		}
		if file.Name != newPath {
			if err := s.Download.RenameFile(ctx, meta.InfoHash, file.Name, newPath); err != nil {
				return err
			}
		}
	}
	return s.Download.Start(ctx, meta.InfoHash)
}

func parse(encoded string) (torrent.Metainfo, error) {
	data, err := decodeTorrent(encoded)
	if err != nil {
		return torrent.Metainfo{}, err
	}
	return torrent.Parse(data)
}

func decodeTorrent(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("种子文件不能为空")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("种子 base64 格式异常: %w", err)
	}
	return data, nil
}

func (s *Service) include(name string, ani model.Ani) bool {
	if strings.HasPrefix(filepath.Base(name), "_____padding_file_") && strings.Contains(name, "BitComet") {
		return false
	}
	mapPattern := func(pattern string) string {
		match := collectionRulePattern.FindStringSubmatch(pattern)
		if len(match) == 0 {
			return pattern
		}
		if match[1] == ani.Subgroup {
			return match[2]
		}
		return ""
	}
	for _, pattern := range ani.Exclude {
		if value := mapPattern(pattern); value != "" && regexpMatch(value, name) {
			return false
		}
	}
	for _, pattern := range ani.Match {
		if value := mapPattern(pattern); value != "" && !regexpMatch(value, name) {
			return false
		}
	}
	if ani.GlobalExclude && s.Config != nil {
		// Snapshot once per call, not once per rule — Snapshot deep-copies the
		// config and include() runs once per file in a collection.
		globalExclude := appconfig.Strings(s.Config.Snapshot(), "exclude")
		for _, pattern := range globalExclude {
			if value := mapPattern(pattern); value != "" && regexpMatch(value, name) {
				return false
			}
		}
	}
	return true
}

func regexpMatch(pattern, value string) bool {
	matched, err := regexutil.MatchString(pattern, value)
	return err == nil && matched
}
func isSubtitle(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "ass", "ssa", "sub", "srt", "lyc", "sup", "pgs", "mks":
		return true
	default:
		return false
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
func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func defaultConfigString(cfg model.Config, key, fallback string) string {
	if value := appconfig.String(cfg, key); value != "" {
		return value
	}
	return fallback
}
