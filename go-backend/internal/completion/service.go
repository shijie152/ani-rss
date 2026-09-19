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
	"strings"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// CompletionTag marks a downloader task whose media has already been
// organized. It mirrors the Java TorrentsTagEnum.DOWNLOAD_COMPLETE value and
// keeps the pass idempotent across intervals.
const CompletionTag = "下载完成"

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
