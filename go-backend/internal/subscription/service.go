// Package subscription owns the subscription collection and its JSON-backed
// HTTP contract. RSS and media modules consume this package rather than files.
package subscription

import (
	"context"
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

	"github.com/mozillazg/go-pinyin"

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
		items[index].Pinyin, items[index].PinyinInitials = titlePinyin(items[index].Title)
	}
	sort.SliceStable(items, func(i, j int) bool {
		sortType := strings.ToUpper(appconfig.String(s.config.Snapshot(), "sortType"))
		switch sortType {
		case "PINYIN":
			if items[i].Pinyin != items[j].Pinyin {
				return items[i].Pinyin < items[j].Pinyin
			}
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
	monthTimes := map[string]time.Time{}
	for index := range items {
		items[index].Sort = index
		date := parseReleaseDate(items[index])
		month := date.Format("2006-01")
		monthTimes[month] = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.Local)
		weekMap[weeks[int(date.Weekday())]] = append(weekMap[weeks[int(date.Weekday())]], items[index])
	}
	months := make([]string, 0, len(monthTimes))
	for month := range monthTimes {
		months = append(months, month)
	}
	sort.Slice(months, func(i, j int) bool { return monthTimes[months[i]].After(monthTimes[months[j]]) })
	resultWeeks := make([]model.WeekAni, 0, len(weeks))
	for _, week := range weekOrder(time.Now().Weekday()) {
		resultWeeks = append(resultWeeks, model.WeekAni{WeekLabel: week, Items: weekMap[week]})
	}
	return model.ListAni{ReleaseDateList: months, WeekList: resultWeeks, Total: len(items)}
}

// EpisodeResolver is injected so total-episode updates remain deterministic
// in tests and the subscription domain does not own external HTTP details.
type EpisodeResolver func(context.Context, model.Ani) (int, error)

// UpdateTotalEpisodes mirrors Java's manual update: without force an existing
// total is retained; otherwise Bangumi's eps value replaces it when present.
func (s *Service) UpdateTotalEpisodes(ctx context.Context, force bool, ids []string, resolve EpisodeResolver) error {
	if len(ids) == 0 {
		return ErrEmptySelection
	}
	if resolve == nil {
		return errors.New("总集数解析器未配置")
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	updates := map[string]int{}
	var failures []string
	for _, item := range s.Items() {
		if !selected[item.ID] || (!force && item.TotalEpisodeNumber > 0) {
			continue
		}
		episodes, err := resolve(ctx, item)
		if err != nil {
			failures = append(failures, item.ID+": "+err.Error())
			continue
		}
		if episodes > 0 && episodes != item.TotalEpisodeNumber {
			updates[item.ID] = episodes
		}
	}
	if len(updates) > 0 {
		s.mu.Lock()
		for index := range s.items {
			if episodes, ok := updates[s.items[index].ID]; ok {
				s.items[index].TotalEpisodeNumber = episodes
			}
		}
		err := s.saveLocked()
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
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

// UpdateCurrentEpisode records the progress derived from a successful RSS
// refresh. Java counts integer episodes from the matched feed, except for
// download-new subscriptions where the highest integer episode is the
// progress value. In coexist mode only the primary RSS contributes.
func (s *Service) UpdateCurrentEpisode(id string, resources []model.Resource) error {
	if strings.TrimSpace(id) == "" {
		return ErrNotFound
	}
	coexist := appconfig.Bool(s.config.Snapshot(), "coexist")
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for candidate, item := range s.items {
		if item.ID == id {
			index = candidate
			break
		}
	}
	if index < 0 {
		return ErrNotFound
	}
	item := s.items[index]
	integerEpisodes := make([]int, 0, len(resources))
	for _, resource := range resources {
		if coexist && !resource.Master {
			continue
		}
		if resource.Episode <= 0 || resource.Episode != float64(int(resource.Episode)) {
			continue
		}
		integerEpisodes = append(integerEpisodes, int(resource.Episode))
	}
	current := 0
	if item.DownloadNew {
		for _, episode := range integerEpisodes {
			if episode > current {
				current = episode
			}
		}
	} else {
		current = len(integerEpisodes)
	}
	if item.CurrentEpisodeNumber == current {
		return nil
	}
	s.items[index].CurrentEpisodeNumber = current
	s.items[index].LastDownloadTime = time.Now().UnixMilli()
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
		// Imported JSON from older installations may not contain an id; Java
		// assigns a fresh id for newly imported subscriptions.
		if strings.TrimSpace(item.ID) == "" {
			item.ID = newID()
		}
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

// ValidateItems checks imported/persisted subscriptions before they enter the
// live service. Keeping this at the domain boundary prevents malformed JSON
// from becoming a silently unusable subscription after restart.
func ValidateItems(items []model.Ani) error {
	ids := make(map[string]struct{}, len(items))
	names := make(map[string]struct{}, len(items))
	for index, item := range items {
		if err := validate(item); err != nil {
			return fmt.Errorf("订阅 %d: %w", index+1, err)
		}
		if _, exists := ids[item.ID]; exists {
			return fmt.Errorf("订阅 %d: ID 重复 %q", index+1, item.ID)
		}
		ids[item.ID] = struct{}{}
		name := strings.ToLower(strings.TrimSpace(item.Title)) + "\x00" + strconv.Itoa(item.Season)
		if _, exists := names[name]; exists {
			return fmt.Errorf("订阅 %d: 标题和季度重复", index+1)
		}
		names[name] = struct{}{}
	}
	return nil
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

func weekOrder(today time.Weekday) []string {
	weeks := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	result := make([]string, 0, len(weeks))
	for day := int(today); day >= 0; day-- {
		result = append(result, weeks[day])
	}
	for day := 6; day > int(today); day-- {
		result = append(result, weeks[day])
	}
	return result
}

func titlePinyin(title string) (string, string) {
	args := pinyin.NewArgs()
	args.Style = pinyin.Normal
	full, initials := strings.Builder{}, strings.Builder{}
	for _, syllable := range pinyin.Pinyin(title, args) {
		if len(syllable) == 0 || syllable[0] == "" {
			continue
		}
		value := strings.ToLower(syllable[0])
		full.WriteString(value)
		initials.WriteString(string([]rune(value)[0]))
	}
	return full.String(), initials.String()
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
