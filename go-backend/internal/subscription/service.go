// Package subscription owns the subscription collection and its JSON-backed
// HTTP contract. RSS and media modules consume this package rather than files.
package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

var (
	ErrEmptySelection = errors.New("未选择订阅")
	ErrNotFound       = errors.New("订阅不存在")
)

type Service struct {
	store  store.Store
	config *appconfig.Manager
	mu     sync.RWMutex
	items  []model.Ani
}

func NewService(s store.Store, config *appconfig.Manager, items []model.Ani) *Service {
	return &Service{store: s, config: config, items: append([]model.Ani(nil), items...)}
}

func (s *Service) List() model.ListAni {
	s.mu.RLock()
	items := append([]model.Ani(nil), s.items...)
	s.mu.RUnlock()
	for index := range items {
		items[index] = normalizeAni(items[index])
	}
	sort.SliceStable(items, func(i, j int) bool {
		sortType := strings.ToUpper(appconfig.String(s.config.Snapshot(), "sortType"))
		switch sortType {
		case "DOWNLOAD_TIME":
			return items[i].LastDownloadTime > items[j].LastDownloadTime
		case "SCORE":
			if items[i].Score != items[j].Score {
				return items[i].Score > items[j].Score
			}
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	weeks := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	weekMap := make(map[string][]model.Ani, len(weeks))
	for _, week := range weeks {
		weekMap[week] = []model.Ani{}
	}
	months := make([]string, 0)
	seenMonths := map[string]bool{}
	for index := range items {
		items[index].Sort = index
		date := parseReleaseDate(items[index])
		month := date.Format("2006-01")
		if !seenMonths[month] {
			months = append(months, month)
			seenMonths[month] = true
		}
		weekMap[weeks[int(date.Weekday())]] = append(weekMap[weeks[int(date.Weekday())]], items[index])
	}
	resultWeeks := make([]model.WeekAni, 0, len(weeks))
	for _, week := range weeks {
		resultWeeks = append(resultWeeks, model.WeekAni{WeekLabel: week, Items: weekMap[week]})
	}
	return model.ListAni{ReleaseDateList: months, WeekList: resultWeeks, Total: len(items)}
}

func (s *Service) Add(item model.Ani) error {
	if err := validate(item); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, existing := range s.items {
		if existing.ID == item.ID {
			return errors.New("此订阅已存在")
		}
		if existing.Title == item.Title && existing.Season == item.Season {
			if !appconfig.Bool(s.config.Snapshot(), "replace") {
				return errors.New("订阅标题重复")
			}
			s.items[index] = item
			return s.saveLocked()
		}
	}
	if item.ReleaseDate == "" {
		item.ReleaseDate = time.Now().Format("2006-01-02")
	}
	if item.Type == "" {
		item.Type = "mikan"
	}
	s.items = append(s.items, item)
	return s.saveLocked()
}

func (s *Service) Set(item model.Ani) error { return s.SetWithMove(item, false) }

func (s *Service) SetWithMove(item model.Ani, move bool) error {
	if err := validate(item); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, existing := range s.items {
		if existing.ID != item.ID && existing.Title == item.Title && existing.Season == item.Season {
			return errors.New("订阅标题重复")
		}
		if existing.ID == item.ID {
			item.CurrentEpisodeNumber = existing.CurrentEpisodeNumber
			item.LastDownloadTime = existing.LastDownloadTime
			s.items[index] = item
			if err := s.saveLocked(); err != nil {
				return err
			}
			if move {
				oldPath, oldErr := pathFor(s.config.Snapshot(), existing, "")
				newPath, newErr := pathFor(s.config.Snapshot(), item, "")
				if oldErr != nil || newErr != nil {
					return errors.New("解析订阅媒体路径失败")
				}
				return moveDirectoryContents(oldPath, newPath)
			}
			return nil
		}
	}
	return ErrNotFound
}

func (s *Service) Delete(ids []string, deleteFiles bool) error {
	if len(ids) == 0 {
		return ErrEmptySelection
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	kept := make([]model.Ani, 0, len(s.items))
	removedItems := make([]model.Ani, 0)
	removed := 0
	for _, item := range s.items {
		if selected[item.ID] {
			removed++
			removedItems = append(removedItems, item)
			continue
		}
		kept = append(kept, item)
	}
	if removed == 0 {
		return ErrNotFound
	}
	s.items = kept
	if err := s.saveLocked(); err != nil {
		return err
	}
	if deleteFiles {
		for _, item := range removedItems {
			path, pathErr := pathFor(s.config.Snapshot(), item, "")
			if pathErr != nil {
				return pathErr
			}
			if err := removeMediaDirectory(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) BatchEnable(value bool, ids []string) error {
	if len(ids) == 0 {
		return ErrEmptySelection
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	found := false
	for index := range s.items {
		if selected[s.items[index].ID] {
			s.items[index].Enable = value
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	return s.saveLocked()
}

func (s *Service) UpdateProgress(force bool, ids []string) error {
	if len(ids) == 0 {
		return ErrEmptySelection
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	for index := range s.items {
		if selected[s.items[index].ID] && (force || s.items[index].TotalEpisodeNumber == 0) && s.items[index].CurrentEpisodeNumber > s.items[index].TotalEpisodeNumber {
			s.items[index].TotalEpisodeNumber = s.items[index].CurrentEpisodeNumber
		}
	}
	return s.saveLocked()
}

func (s *Service) DownloadPath(item model.Ani) (map[string]any, error) {
	path, err := pathFor(s.config.Snapshot(), item, "")
	if err != nil {
		return nil, err
	}
	oldPath := path
	s.mu.RLock()
	for _, existing := range s.items {
		if existing.ID == item.ID {
			oldPath, _ = pathFor(s.config.Snapshot(), existing, "")
			break
		}
	}
	s.mu.RUnlock()
	return map[string]any{"change": path != oldPath, "downloadPath": path}, nil
}

// DownloadPathWithTemplate resolves a user-selected completed/library
// template using the exact same substitutions as the normal download path.
func (s *Service) DownloadPathWithTemplate(item model.Ani, template string) (string, error) {
	return pathFor(s.config.Snapshot(), item, template)
}

func (s *Service) Import(items []model.Ani, conflict string) error {
	if len(items) == 0 {
		return ErrEmptySelection
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		if err := validate(item); err != nil {
			return err
		}
		found := -1
		for index, existing := range s.items {
			if existing.Title == item.Title && existing.Season == item.Season {
				found = index
				break
			}
		}
		if found < 0 {
			if item.ID == "" {
				item.ID = newID()
			}
			s.items = append(s.items, item)
		} else if strings.EqualFold(conflict, "SKIP") {
			continue
		} else {
			item.ID = s.items[found].ID
			item.CurrentEpisodeNumber = s.items[found].CurrentEpisodeNumber
			item.LastDownloadTime = s.items[found].LastDownloadTime
			s.items[found] = item
		}
	}
	return s.saveLocked()
}

func (s *Service) Items() []model.Ani {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.Ani(nil), s.items...)
}

func (s *Service) saveLocked() error { return s.store.SaveSubscriptions(s.items) }

func validate(item model.Ani) error {
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("订阅ID不能为空")
	}
	if strings.TrimSpace(item.Title) == "" {
		return errors.New("订阅标题不能为空")
	}
	if strings.TrimSpace(item.URL) == "" {
		return errors.New("RSS地址不能为空")
	}
	if item.Season < 0 {
		return errors.New("季度不能为负数")
	}
	return nil
}

func normalizeAni(item model.Ani) model.Ani {
	if item.ReleaseDate == "" {
		item.ReleaseDate = time.Now().Format("2006-01-02")
	}
	return item
}

func parseReleaseDate(item model.Ani) time.Time {
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
		if date, err := time.Parse(layout, item.ReleaseDate); err == nil {
			return date
		}
	}
	if item.Year > 0 && item.Month > 0 && item.Date > 0 {
		return time.Date(item.Year, time.Month(item.Month), item.Date, 0, 0, 0, 0, time.Local)
	}
	return time.Now()
}

func pathFor(cfg model.Config, item model.Ani, override string) (string, error) {
	template := appconfig.String(cfg, "downloadPathTemplate")
	if item.OVA {
		if value := appconfig.String(cfg, "ovaDownloadPathTemplate"); value != "" {
			template = value
		}
	}
	if item.CustomDownloadPath && item.CustomDownloadPathTemplate != "" {
		for _, value := range strings.Split(item.CustomDownloadPathTemplate, "\n") {
			if strings.TrimSpace(value) != "" {
				template = strings.TrimSpace(value)
				break
			}
		}
	}
	if strings.TrimSpace(override) != "" {
		template = override
	}
	if template == "" {
		return "", errors.New("下载路径模板不能为空")
	}
	release := parseReleaseDate(item)
	year := release.Year()
	month := int(release.Month())
	tmdbYear := item.Year
	if value, ok := item.TMDB["first_air_date"].(string); ok && len(value) >= 4 {
		if parsed, parseErr := strconv.Atoi(value[:4]); parseErr == nil {
			tmdbYear = parsed
		}
	}
	if tmdbYear == 0 {
		tmdbYear = year
	}
	quarter, quarterName := quarter(release.Month())
	replacements := map[string]string{
		"${title}": item.Title, "${themoviedbName}": item.TheMovieDBName, "${jpTitle}": item.JPTitle,
		"${season}": fmt.Sprintf("%d", item.Season), "${seasonFormat}": fmt.Sprintf("%02d", item.Season),
		"${year}": fmt.Sprintf("%d", year), "${month}": fmt.Sprintf("%d", month), "${monthFormat}": fmt.Sprintf("%02d", month),
		"${tmdbYear}": fmt.Sprintf("%d", tmdbYear), "${quarter}": fmt.Sprintf("%d", quarter),
		"${quarterFormat}": fmt.Sprintf("%02d", quarter), "${quarterName}": quarterName,
		"${tmdbid}": mapString(item.TMDB, "id"), "${bgmId}": subjectID(item.BGMURL),
		"${subgroup}": item.Subgroup, "${letter}": initial(item.Title),
	}
	for key, value := range replacements {
		template = strings.ReplaceAll(template, key, value)
	}
	path, err := filepath.Abs(filepath.Clean(template))
	if err != nil {
		return "", err
	}
	return path, nil
}

func quarter(month time.Month) (int, string) {
	switch month {
	case time.December:
		return 1, "冬"
	case time.January, time.February:
		return 1, "冬"
	case time.March, time.April, time.May:
		return 4, "春"
	case time.June, time.July, time.August:
		return 7, "夏"
	default:
		return 10, "秋"
	}
}

func initial(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, runeValue := range value {
		if (runeValue >= 'a' && runeValue <= 'z') || (runeValue >= 'A' && runeValue <= 'Z') {
			return strings.ToUpper(string(runeValue))
		}
	}
	return string([]rune(value)[0])
}

func mapString(value map[string]any, key string) string {
	switch typed := value[key].(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func subjectID(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := range parts {
		if parts[index] == "subject" && index+1 < len(parts) {
			return parts[index+1]
		}
	}
	return parsed.Query().Get("subject")
}

func moveDirectoryContents(source, target string) error {
	if filepath.Clean(source) == filepath.Clean(target) {
		return nil
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from, to := filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())
		if _, err := os.Stat(to); err == nil {
			return fmt.Errorf("目标文件已存在: %s", to)
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return os.Remove(source)
}

func removeMediaDirectory(path string) error {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return err
	}
	if path == string(filepath.Separator) || path == "." || path == "" {
		return errors.New("拒绝删除无效媒体路径")
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func newID() string { return fmt.Sprintf("ani-%d", time.Now().UnixNano()) }
