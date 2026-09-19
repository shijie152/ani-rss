package rss

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
)

// Downloader is the narrow side-effect boundary needed to submit RSS
// resources. Protocol adapters may implement more operations, but submission
// only depends on these four.
type Downloader interface {
	Login(context.Context) error
	Add(context.Context, model.Resource, string, []string, bool) error
	Torrents(context.Context) ([]model.Torrent, error)
	Delete(context.Context, string, bool) error
}

// Submitter owns resource submission and its side effects. PlanSubmission is
// pure and can be tested without a downloader, while Submit performs the
// externally visible operations in the established order.
type Submitter struct {
	Config       appconfig.Reader
	DownloadPath func(model.Ani) (map[string]any, error)
	SaveCache    func(context.Context, *http.Client, string, model.Ani, model.Resource) error
	History      store.HistoryStore
	Downloader   Downloader
	Notify       func(context.Context, model.Ani, *model.Resource, string, string) error
	ConfigDir    string
	HTTPClient   *http.Client
	// LocalExists reports whether a resource's target media file is already on
	// disk. It backs the Java fileExist toggle; nil disables the check.
	LocalExists func(model.Ani, model.Resource) bool
	// SaveExist marks a resource already on disk so later passes short-circuit
	// without rescanning the media directory (Java TorrentUtil.saveTorrent).
	SaveExist func(context.Context, model.Ani, model.Resource)
	// Reserve/Release provide a process-local in-flight seam. They protect
	// duplicate submissions without holding a lock across network I/O.
	Reserve func(string) bool
	Release func(string)
}

// PlanSubmission returns only resources that are absent from durable history
// and the current downloader inventory. Matching by both hash and title keeps
// retries idempotent when a downloader created a task before losing its reply.
func PlanSubmission(resources []model.Resource, history []model.Resource, tasks []model.Torrent) []model.Resource {
	existing := map[string]bool{}
	for _, item := range history {
		existing[resourceKey(item)] = true
	}
	for _, task := range tasks {
		if task.Hash != "" {
			existing[strings.ToLower(task.Hash)] = true
		}
	}
	result := make([]model.Resource, 0, len(resources))
	for _, resource := range resources {
		key := resourceKey(resource)
		if existing[key] {
			continue
		}
		duplicateTask := false
		for _, task := range tasks {
			if (resource.InfoHash != "" && strings.EqualFold(task.Hash, resource.InfoHash)) || strings.EqualFold(task.Name, resource.Title) {
				duplicateTask = true
				break
			}
		}
		if duplicateTask {
			existing[key] = true
			continue
		}
		result = append(result, resource)
		existing[key] = true
	}
	return result
}

func (s *Submitter) Submit(ctx context.Context, ani model.Ani, resources []model.Resource) ([]model.Resource, error) {
	if s == nil || s.Downloader == nil {
		return nil, errors.New("下载器未配置")
	}
	if err := s.Downloader.Login(ctx); err != nil {
		return nil, err
	}
	var history []model.Resource
	if s.History != nil {
		history, _ = s.History.LoadResources()
	}
	tasks, err := s.Downloader.Torrents(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询下载器任务失败: %w", err)
	}
	if s.DownloadPath == nil {
		return nil, errors.New("下载路径解析器未配置")
	}
	pathData, err := s.DownloadPath(ani)
	if err != nil {
		return nil, err
	}
	savePath, ok := pathData["downloadPath"].(string)
	if !ok {
		return nil, errors.New("下载路径异常")
	}
	var failures []string
	if deleteErr := s.deleteStandbyTasks(ctx, savePath, resources, tasks); deleteErr != nil {
		failures = append(failures, deleteErr.Error())
	}
	// downloadCount caps concurrent unfinished downloads exactly like Java's
	// downloadAni: count current unfinished tasks, then stop once the limit is
	// reached. Main-integer resources consume a slot; .5/standby do not.
	downloadCount := 0
	unfinished := 0
	if s.Config != nil {
		downloadCount = appconfig.Int(s.Config.Snapshot(), "downloadCount")
	}
	if downloadCount > 0 {
		for _, task := range tasks {
			if !task.Finished() {
				unfinished++
			}
		}
	}
	newResources := []model.Resource{}
	for _, resource := range PlanSubmission(resources, history, tasks) {
		if s.localExists(ani, resource) {
			continue
		}
		if downloadCount > 0 && unfinished >= downloadCount {
			continue
		}
		key := resourceKey(resource)
		if s.Reserve != nil && !s.Reserve(key) {
			continue
		}
		if s.Release != nil {
			defer s.Release(key)
		}
		tags := []string{"ani-rss"}
		if resource.Subgroup != "" {
			tags = append(tags, resource.Subgroup)
		}
		if !resource.Master {
			tags = append(tags, "备用RSS")
		}
		if err := s.Downloader.Add(ctx, resource, savePath, tags, false); err != nil {
			if recovered, inventoryErr := s.Downloader.Torrents(ctx); inventoryErr == nil && containsResourceTask(recovered, resource) {
				history = append(history, resource)
				s.recordCache(ctx, ani, resource, &failures)
				newResources = append(newResources, resource)
				if downloadCount > 0 && resource.Master && resource.Episode == float64(int(resource.Episode)) {
					unfinished++
				}
				if s.Notify != nil {
					if notifyErr := s.Notify(ctx, ani, &resource, "DOWNLOAD_START", "开始下载: "+resource.Title); notifyErr != nil {
						failures = append(failures, "通知失败: "+notifyErr.Error())
					}
				}
				continue
			}
			failures = append(failures, resource.Title+": "+err.Error())
			continue
		}
		history = append(history, resource)
		s.recordCache(ctx, ani, resource, &failures)
		newResources = append(newResources, resource)
		if downloadCount > 0 && resource.Master && resource.Episode == float64(int(resource.Episode)) {
			unfinished++
		}
		if s.Notify != nil {
			if notifyErr := s.Notify(ctx, ani, &resource, "DOWNLOAD_START", "开始下载: "+resource.Title); notifyErr != nil {
				failures = append(failures, "通知失败: "+notifyErr.Error())
			}
		}
	}
	if s.History != nil {
		if err := s.History.SaveResources(history); err != nil {
			return newResources, err
		}
	}
	if len(failures) > 0 {
		return newResources, errors.New(strings.Join(failures, "; "))
	}
	return newResources, nil
}

func (s *Submitter) recordCache(ctx context.Context, ani model.Ani, resource model.Resource, failures *[]string) {
	if s.ConfigDir == "" || s.SaveCache == nil {
		return
	}
	if err := s.SaveCache(ctx, s.HTTPClient, s.ConfigDir, ani, resource); err != nil {
		*failures = append(*failures, resource.Title+": 缓存资源失败: "+err.Error())
	}
}

func (s *Submitter) deleteStandbyTasks(ctx context.Context, savePath string, resources []model.Resource, tasks []model.Torrent) error {
	if s.Config == nil || !appconfig.Bool(s.Config.Snapshot(), "delete") || !appconfig.Bool(s.Config.Snapshot(), "deleteStandbyRSSOnly") || !appconfig.Bool(s.Config.Snapshot(), "standbyRss") || appconfig.Bool(s.Config.Snapshot(), "coexist") {
		return nil
	}
	primaryEpisodes := map[float64]bool{}
	for _, resource := range resources {
		if resource.Master && resource.Episode > 0 {
			primaryEpisodes[resource.Episode] = true
		}
	}
	if len(primaryEpisodes) == 0 {
		return nil
	}
	var failures []string
	for _, task := range tasks {
		if task.Hash == "" || task.SavePath != savePath || !hasTorrentTag(task.TagList, "备用RSS") || !primaryEpisodes[Episode(task.Name)] {
			continue
		}
		if err := s.Downloader.Delete(ctx, task.Hash, true); err != nil {
			failures = append(failures, task.Hash+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New("备用 RSS 清理失败: " + strings.Join(failures, "; "))
	}
	return nil
}

func containsResourceTask(tasks []model.Torrent, resource model.Resource) bool {
	for _, task := range tasks {
		if resource.InfoHash != "" && strings.EqualFold(task.Hash, resource.InfoHash) {
			return true
		}
		if strings.EqualFold(task.Name, resource.Title) {
			return true
		}
	}
	return false
}

func hasTorrentTag(tags []string, target string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), target) {
			return true
		}
	}
	return false
}

func resourceKey(resource model.Resource) string {
	if resource.InfoHash != "" {
		return strings.ToLower(resource.InfoHash)
	}
	sum := sha256.Sum256([]byte(resource.DownloadURL + "\x00" + resource.Title))
	return hex.EncodeToString(sum[:])
}

// localExists applies the Java fileExist check: when the toggle is on and a
// resolver is wired, a resource whose media file already sits in the download
// directory is skipped and marked so later passes short-circuit cheaply.
func (s *Submitter) localExists(ani model.Ani, resource model.Resource) bool {
	if s.Config == nil || !appconfig.Bool(s.Config.Snapshot(), "fileExist") {
		return false
	}
	if s.LocalExists == nil || !s.LocalExists(ani, resource) {
		return false
	}
	if s.SaveExist != nil {
		s.SaveExist(context.Background(), ani, resource)
	}
	return true
}
