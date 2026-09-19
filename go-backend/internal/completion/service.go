// Package completion owns the download-completion lifecycle that the Java
// RenameTask drove on a fixed interval: poll the configured downloader, pick
// up every finished task, organize it into the media library, notify, and
// retire the subscription or seeding task as configured.
//
// The HTTP boundary is intentionally untouched; this module is reached only
// through the scheduler and the downloader/media/notification seams.
package completion

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// CompletionTag marks a downloader task whose media has already been
// organized. It mirrors the Java TorrentsTagEnum.DOWNLOAD_COMPLETE value and
// keeps the pass idempotent across intervals.
const CompletionTag = "下载完成"

// RenameTag marks a task whose in-downloader files were already renamed. It
// mirrors Java's TorrentsTagEnum.RENAME so the pass does not rename twice.
const RenameTag = "RENAME"

// TaskFile is one file inside a torrent task, the subset needed for the
// in-downloader rename.
type TaskFile struct {
	Index    int
	Name     string
	Size     int64
	Priority int
}

// TaskFileLister lists a task's member files; nil disables the rename.
type TaskFileLister func(context.Context, string) ([]TaskFile, error)

// TaskFileRenamer renames one member inside the downloader.
type TaskFileRenamer func(context.Context, string, string, string) error

// TaskFilePriority sets a member's download priority (0 drops a duplicate).
type TaskFilePriority func(context.Context, string, int, int) error

// Downloader is the narrow slice of the adapter used by the completion pass.
// Implementations are shared with the RSS submission adapter.
type Downloader interface {
	Login(context.Context) error
	Torrents(context.Context) ([]model.Torrent, error)
	AddTags(context.Context, string, string) error
	Delete(context.Context, string, bool) error
}

// ScrapeResult is the observable outcome of media organization. Only the
// fields the completion pass needs are exposed.
type ScrapeResult struct {
	Processed int
	Path      string
}

// Scraper organizes a finished download into the media library. It is the
// post-download half of the Java rename+scrape chain.
type Scraper interface {
	Scrape(context.Context, *model.Ani, bool) (ScrapeResult, error)
}

// NotificationEvent is the dispatcher-facing payload. The completion module
// stays decoupled from the notification package's Event type so tests can
// assert on the emitted status directly.
type NotificationEvent struct {
	Ani      model.Ani
	Status   string
	Text     string
	Path     string
	Resource *model.Resource
}

// Notifier dispatches one event through the configured notification pipeline.
type Notifier func(context.Context, NotificationEvent) error

// Subscriptions is the read/write slice of the subscription service the pass
// needs to locate the owning subscription and to retire a completed one.
type Subscriptions interface {
	Items() []model.Ani
	DownloadPath(model.Ani) (map[string]any, error)
	BatchEnable(bool, []string) error
}

// Coordinator runs one completion pass per scheduler tick.
type Coordinator struct {
	Config        model.Config
	Downloader    Downloader
	Scraper       Scraper
	Subscriptions Subscriptions
	Notify        Notifier
	Logger        *slog.Logger
	// ListFiles/RenameFileInTask/SetPriority wire the optional in-downloader
	// rename. They are nil for adapters that cannot enumerate task files.
	ListFiles        TaskFileLister
	RenameFileInTask TaskFileRenamer
	SetPriority      TaskFilePriority
}

// Run executes a single completion pass. It is safe to invoke on a fixed
// interval; per-task failures are logged and never abort the round, matching
// the Java task's resilience.
func (c *Coordinator) Run(ctx context.Context) error {
	if c == nil || c.Downloader == nil {
		return errors.New("下载器未配置")
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if err := c.Downloader.Login(ctx); err != nil {
		return err
	}
	tasks, err := c.Downloader.Torrents(ctx)
	if err != nil {
		return err
	}
	var failures []string
	for _, task := range tasks {
		if err := c.process(ctx, task); err != nil {
			failures = append(failures, task.Name+": "+err.Error())
			c.Logger.Warn("download completion handling failed", "task", task.Name, "error", err)
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// process handles one finished downloader task: resolve its subscription,
// mark it complete, organize media, notify, finish, and optionally delete.
func (c *Coordinator) process(ctx context.Context, task model.Torrent) error {
	if !finished(task) {
		return nil
	}
	if hasTag(task.TagList, CompletionTag) {
		return nil
	}
	ani, ok := c.findAni(task)
	if !ok {
		c.Logger.Debug("no subscription for completed task", "task", task.Name, "savePath", task.SavePath)
		return nil
	}
	ani.Subgroup = c.subgroupFor(task, ani)

	// Mark before organizing so a retried tick cannot double-notify even if a
	// later step fails. The tag is the durable idempotency record.
	if err := c.Downloader.AddTags(ctx, task.Hash, CompletionTag); err != nil {
		return err
	}

	// In-downloader rename (Java TorrentUtil.rename): give the seeding files
	// the canonical names before the media library organizes them. Best-effort
	// — adapters without file access simply skip it.
	if err := c.renameTaskFiles(ctx, task, ani); err != nil {
		c.Logger.Warn("in-downloader rename failed", "task", task.Name, "error", err)
	}

	organized, scrapeErr := c.organize(ctx, &ani)
	if scrapeErr != nil {
		// Scrape failure must not suppress the completion notification or the
		// deletion decision, matching Java's logged-and-continue behaviour.
		c.Logger.Warn("scrape failed for completed task", "subscription", ani.ID, "error", scrapeErr)
	}

	if organized == 0 && c.Notify != nil {
		// media.Service already emits DOWNLOAD_END when it processed files;
		// when nothing was organized we still owe the completion signal.
		text := task.Name + " 下载完成"
		if hasTag(task.TagList, "备用RSS") {
			text = "(备用RSS) " + text
		}
		if err := c.Notify(ctx, NotificationEvent{Ani: ani, Status: "DOWNLOAD_END", Text: text}); err != nil {
			c.Logger.Warn("completion notification failed", "subscription", ani.ID, "error", err)
		}
	}

	c.finishSubscription(ctx, ani)

	if err := c.deleteTask(ctx, task); err != nil {
		return err
	}
	return nil
}

// organize runs media scraping when the scrape toggle is on, returning the
// number of files organized.
func (c *Coordinator) organize(ctx context.Context, ani *model.Ani) (int, error) {
	if !appconfig.Bool(c.Config, "scrape") || c.Scraper == nil {
		return 0, nil
	}
	result, err := c.Scraper.Scrape(ctx, ani, false)
	if err != nil {
		return result.Processed, err
	}
	return result.Processed, nil
}

// finishSubscription applies the Java autoDisabled/completed lifecycle: when
// the subscription reached its total episode count it is notified and
// disabled so the scheduler stops tracking it.
func (c *Coordinator) finishSubscription(ctx context.Context, ani model.Ani) {
	if !appconfig.Bool(c.Config, "autoDisabled") || !appconfig.Bool(c.Config, "completed") {
		return
	}
	if !ani.Completed || ani.TotalEpisodeNumber < 1 || ani.CurrentEpisodeNumber < ani.TotalEpisodeNumber {
		return
	}
	if c.Notify != nil {
		if err := c.Notify(ctx, NotificationEvent{Ani: ani, Status: "COMPLETED", Text: ani.Title + " 订阅已完结"}); err != nil {
			c.Logger.Warn("completion notice failed", "subscription", ani.ID, "error", err)
		}
	}
	if c.Subscriptions != nil {
		if err := c.Subscriptions.BatchEnable(false, []string{ani.ID}); err != nil {
			c.Logger.Warn("failed to disable completed subscription", "subscription", ani.ID, "error", err)
		}
	}
}

// deleteTask retires the seeding task per Java's RenameTask+TorrentUtil.delete:
// the delete toggle must be on, deleteStandbyRSSOnly skips entirely (standby
// tasks are retired by the RSS pass instead), and the task must be eligible
// for deletion (awaitStalledUP requires a fully seeded stoppedUP state).
func (c *Coordinator) deleteTask(ctx context.Context, task model.Torrent) error {
	if !appconfig.Bool(c.Config, "delete") {
		return nil
	}
	if appconfig.Bool(c.Config, "deleteStandbyRSSOnly") {
		return nil
	}
	if !c.allowDelete(task) {
		return nil
	}
	return c.Downloader.Delete(ctx, task.Hash, false)
}

// allowDelete mirrors TorrentUtil.allowDelete: when awaitStalledUP is set the
// task must have fully seeded (stoppedUP); otherwise any finished state is
// eligible.
func (c *Coordinator) allowDelete(task model.Torrent) bool {
	if appconfig.Bool(c.Config, "awaitStalledUP") {
		return task.State == "stoppedUP"
	}
	return task.Finished()
}

// findAni locates the subscription whose resolved download path owns the
// task's save path, matching Java's findAniByDownloadPath.
func (c *Coordinator) findAni(task model.Torrent) (model.Ani, bool) {
	if c.Subscriptions == nil {
		return model.Ani{}, false
	}
	for _, item := range c.Subscriptions.Items() {
		data, err := c.Subscriptions.DownloadPath(item)
		if err != nil {
			continue
		}
		path, _ := data["downloadPath"].(string)
		if path != "" && samePath(path, task.SavePath) {
			return item, true
		}
	}
	return model.Ani{}, false
}

// subgroupFor prefers a standby-RSS label carried on the task's tags, falling
// back to the subscription's own subgroup then the placeholder.
func (c *Coordinator) subgroupFor(task model.Torrent, ani model.Ani) string {
	labels := map[string]bool{}
	for _, standby := range ani.StandbyRSSList {
		labels[standby.Label] = true
	}
	for _, tag := range task.TagList {
		if labels[tag] {
			return tag
		}
	}
	if strings.TrimSpace(ani.Subgroup) != "" {
		return ani.Subgroup
	}
	return "未知字幕组"
}

// renameTaskFiles mirrors Java's TorrentUtil.rename/DOWNLOAD.rename: when the
// rename toggle is on and the adapter can list task files, video and subtitle
// members are renamed inside the downloader to the task's canonical name so
// seeded media keeps the standard naming. OVA/other tasks skip it. The
// RENAME tag is applied once files are renamed so later passes do not repeat.
func (c *Coordinator) renameTaskFiles(ctx context.Context, task model.Torrent, ani model.Ani) error {
	if !appconfig.Bool(c.Config, "rename") || hasTag(task.TagList, RenameTag) {
		return nil
	}
	if c.ListFiles == nil || c.RenameFileInTask == nil {
		return nil
	}
	files, err := c.ListFiles(ctx, task.Hash)
	if err != nil {
		return err
	}
	// Java's files(filter=true): only non-empty video/subtitle members take
	// part, largest first so the primary video wins the canonical name.
	mediaFiles := make([]TaskFile, 0, len(files))
	for _, f := range files {
		if f.Size < 1 || (!isVideoName(f.Name) && !isSubtitleName(f.Name)) {
			continue
		}
		mediaFiles = append(mediaFiles, f)
	}
	sort.SliceStable(mediaFiles, func(i, j int) bool { return mediaFiles[i].Size > mediaFiles[j].Size })
	files = mediaFiles
	if len(files) == 0 {
		return nil
	}
	reName := task.Name
	if strings.TrimSpace(reName) == "" {
		return nil
	}
	subFolder := ""
	if appconfig.Bool(c.Config, "subtitleIndependentFolderEnabled") {
		subFolder = strings.TrimSpace(appconfig.String(c.Config, "subtitleIndependentFolderName"))
	}
	existing := map[string]bool{}
	for _, f := range files {
		existing[f.Name] = true
	}
	used := map[string]bool{}
	for _, f := range files {
		newName := fileReName(f.Name, reName)
		if newName == f.Name {
			continue
		}
		if isSubtitleName(newName) && subFolder != "" {
			newName = subFolder + "/" + newName
		}
		if existing[newName] || used[newName] {
			// A duplicate target means the member is redundant; stop its
			// download like Java's filePrio=0 rather than renaming onto it.
			if c.SetPriority != nil {
				_ = c.SetPriority(ctx, task.Hash, f.Index, 0)
			}
			continue
		}
		used[newName] = true
		if err := c.RenameFileInTask(ctx, task.Hash, f.Name, newName); err != nil {
			return err
		}
	}
	return c.Downloader.AddTags(ctx, task.Hash, RenameTag)
}

// fileReName reproduces BaseDownload.getFileReName: video keeps its
// extension, subtitle keeps an optional language segment plus its extension,
// and any other file is left untouched.
func fileReName(name, reName string) string {
	ext := extension(name)
	if ext == "" {
		return name
	}
	var newPath string
	switch {
	case isVideoName(name):
		newPath = reName + "." + ext
	case isSubtitleName(name):
		newPath = reName
		if inner := extension(strings.TrimSuffix(name, "."+ext)); inner != "" {
			newPath += "." + inner
		}
		newPath += "." + ext
	default:
		return name
	}
	return newPath
}

func extension(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	return name[idx+1:]
}

func isVideoName(name string) bool {
	switch strings.ToLower(extension(name)) {
	case "mkv", "mp4", "avi", "wmv", "ts", "m4v", "mov", "flv":
		return true
	}
	return false
}

func isSubtitleName(name string) bool {
	switch strings.ToLower(extension(name)) {
	case "ass", "ssa", "sub", "srt", "lyc", "sup", "pgs", "mks", "vtt":
		return true
	}
	return false
}

// finished delegates to model.Torrent.Finished so the terminal-state set is
// defined once and shared with the submission-side concurrency count.
func finished(task model.Torrent) bool {
	return task.Finished()
}

func hasTag(tags []string, target string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), target) {
			return true
		}
	}
	return false
}

func samePath(left, right string) bool {
	l, lerr := filepath.Abs(filepath.Clean(left))
	r, rerr := filepath.Abs(filepath.Clean(right))
	return lerr == nil && rerr == nil && l == r
}
