// Package media handles the filesystem part of the download-to-library path.
// It deliberately has no dependency on a downloader: completed files can be
// processed after a restart or by a manually triggered scrape.
package media

import (
	"context"
	"crypto/md5"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/metadata"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

type PathResolver func(model.Ani) (string, error)

type Service struct {
	Config       appconfig.Reader
	ConfigDir    string
	Metadata     *metadata.Client
	HTTPClient   *http.Client
	ResolvePath  PathResolver
	ResolveOther func(model.Ani, string) (string, error)
	Logger       *slog.Logger
}

// Reader is the small part of config.Manager needed by the media service.
// Keeping it here makes filesystem tests independent from JSON persistence.
type Result struct {
	Ani       model.Ani         `json:"ani"`
	Metadata  model.Metadata    `json:"metadata"`
	Path      string            `json:"path"`
	Processed int               `json:"processed"`
	Files     []model.MediaFile `json:"files,omitempty"`
	Errors    []string          `json:"errors,omitempty"`
}

type mediaIdentity struct {
	Episode float64
	Season  int
	HasEp   bool
}

var (
	seasonEpisodePattern = regexp.MustCompile(`(?i)(?:^|[^a-z])s(\d{1,3})[ ._-]*e(\d+(?:\.5)?)(?:[^0-9]|$)`)
	episodePattern       = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:e|ep|episode|第)[ ._-]*(\d+(?:\.5)?)(?:[^0-9]|$)`)
	bracketEpisode       = regexp.MustCompile(`(?:\[|\s|-)(\d+(?:\.5)?)(?:\]|\s|$)`)
)

func New(config appconfig.Reader, metadataClient *metadata.Client, client *http.Client, resolver PathResolver) *Service {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	logger := slog.Default()
	return &Service{Config: config, Metadata: metadataClient, HTTPClient: client, ResolvePath: resolver, Logger: logger}
}

func (s *Service) Scrape(ctx context.Context, ani *model.Ani, force bool) (Result, error) {
	if ani == nil {
		return Result{}, errors.New("订阅不能为空")
	}
	if s.ResolvePath == nil {
		return Result{}, errors.New("媒体路径解析器未配置")
	}
	if s.Metadata == nil {
		return Result{}, errors.New("元数据客户端未配置")
	}
	if s.Config != nil && !appconfig.Bool(s.Config.Snapshot(), "tmdb") && strings.TrimSpace(ani.BGMURL) == "" {
		return Result{}, errors.New("未配置 TMDB 或 Bangumi subject")
	}
	path, err := s.ResolvePath(*ani)
	if err != nil {
		return Result{}, err
	}
	result := Result{Ani: *ani, Path: path, Files: []model.MediaFile{}}
	metadataValue, raw, lookupErr := s.Metadata.Lookup(ctx, *ani)
	if lookupErr != nil {
		return result, lookupErr
	}
	result.Metadata = metadataValue
	if raw != nil && metadataValue.ID != "" {
		ani.TMDB = raw
	}
	ani.TheMovieDBName = FinalTitle(metadataValue, s.Config)
	if metadataValue.Poster != "" && ani.Image == "" {
		ani.Image = s.imageURL(metadataValue.Poster)
	}
	result.Ani = *ani
	if err := os.MkdirAll(path, 0o755); err != nil {
		return result, fmt.Errorf("创建媒体目录: %w", err)
	}
	processed, processErrs := s.processDirectory(path, *ani, metadataValue, force)
	result.Processed, result.Errors = processed, processErrs
	result.Files, _ = s.List(path)
	if len(processErrs) > 0 {
		return result, fmt.Errorf("媒体整理部分失败: %s", strings.Join(processErrs, "; "))
	}
	if completedPath, moveErr := s.moveCompleted(path, *ani); moveErr != nil {
		result.Errors = append(result.Errors, moveErr.Error())
	} else if completedPath != "" {
		result.Path = completedPath
	}
	return result, nil
}

func (s *Service) List(path string) ([]model.MediaFile, error) {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []model.MediaFile{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("媒体路径不是目录")
	}
	files := make([]model.MediaFile, 0)
	err = filepath.WalkDir(path, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !isVideo(entry.Name()) {
			return nil
		}
		stat, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		identity := parseIdentity(entry.Name())
		file := model.MediaFile{Title: entry.Name(), Filename: filepath.ToSlash(filePath), Name: entry.Name(), LastModify: stat.ModTime().UnixMilli(), Episode: identity.Episode, FormatSize: formatSize(stat.Size()), ExtName: strings.ToLower(filepath.Ext(entry.Name()))[1:]}
		file.Subtitles = subtitlesFor(filePath)
		files = append(files, file)
		return nil
	})
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Episode != files[j].Episode {
			return files[i].Episode < files[j].Episode
		}
		return files[i].Filename < files[j].Filename
	})
	return files, err
}

// PlaybackList applies the same minimum-size rule as the current Java player
// endpoint. Scraping still scans small fixture/placeholder files so metadata
// generation remains independent from playback policy.
func (s *Service) PlaybackList(path string) ([]model.MediaFile, error) {
	files, err := s.List(path)
	if err != nil {
		return nil, err
	}
	result := make([]model.MediaFile, 0, len(files))
	for _, file := range files {
		info, statErr := os.Stat(filepath.FromSlash(file.Filename))
		if statErr == nil && info.Size() >= 20*1024*1024 {
			result = append(result, file)
		}
	}
	return result, nil
}

// SubtitlesFor exposes the sidecar matching rule used by both the playlist
// response and the file player endpoint.
func SubtitlesFor(videoPath string) []model.SubtitleInfo { return subtitlesFor(videoPath) }

func IsVideo(name string) bool { return isVideo(name) }
func IsSupported(name string) bool {
	return isVideo(name) || isSubtitle(name) || hasExt(name, map[string]bool{"jpg": true, "jpeg": true, "png": true, "webp": true, "svg": true})
}

func (s *Service) RefreshCover(ctx context.Context, imageURL string, override bool) (string, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return "cover.png", nil
	}
	parsed, err := url.Parse(imageURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("图片地址格式异常")
	}
	configDir := s.ConfigDir
	if configDir == "" {
		configDir = "config"
	}
	filesDir := filepath.Join(configDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return "", err
	}
	hash := fmt.Sprintf("%x", md5.Sum([]byte(imageURL)))
	ext := strings.ToLower(filepath.Ext(parsed.Path))
	if ext == "" || len(ext) > 8 {
		ext = ".jpg"
	}
	name := hash + ext
	relative := filepath.ToSlash(filepath.Join(string(name[0]), name))
	target := filepath.Join(filesDir, relative)
	if !override {
		if _, err := os.Stat(target); err == nil {
			return relative, nil
		}
	}
	response, err := s.HTTPClient.Get(imageURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("图片服务返回 HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".cover-*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := io.Copy(temp, io.LimitReader(response.Body, 16<<20)); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tempName, target); err != nil {
		return "", err
	}
	ok = true
	return relative, nil
}

func (s *Service) processDirectory(path string, ani model.Ani, metadataValue model.Metadata, force bool) (int, []string) {
	files, err := s.videoFiles(path)
	if err != nil {
		return 0, []string{err.Error()}
	}
	processed := 0
	errorsFound := []string{}
	for _, source := range files {
		identity := parseIdentityFor(filepath.Base(source), ani)
		if !ani.OVA && !identity.HasEp {
			continue
		}
		if ani.OVA {
			identity.Episode = 1
		}
		name := buildFilename(ani, metadataValue, filepath.Base(source), identity, s.Config)
		target := filepath.Join(filepath.Dir(source), name+strings.ToLower(filepath.Ext(source)))
		if filepath.Clean(source) != filepath.Clean(target) {
			if _, statErr := os.Stat(target); statErr == nil {
				errorsFound = append(errorsFound, "目标文件已存在: "+target)
				continue
			} else if !errors.Is(statErr, os.ErrNotExist) {
				errorsFound = append(errorsFound, statErr.Error())
				continue
			}
			if err := os.Rename(source, target); err != nil {
				errorsFound = append(errorsFound, fmt.Sprintf("重命名 %s: %v", source, err))
				continue
			}
			moveSidecars(source, target, &errorsFound)
		}
		if err := s.writeNFO(filepath.Dir(target), target, ani, metadataValue, identity, force); err != nil {
			errorsFound = append(errorsFound, err.Error())
			continue
		}
		processed++
	}
	showPath := path
	if !ani.OVA {
		showPath = filepath.Dir(path)
	}
	if err := s.writeShowAssets(showPath, ani, metadataValue, force); err != nil {
		errorsFound = append(errorsFound, err.Error())
	}
	if !ani.OVA {
		if err := writeXML(filepath.Join(showPath, "tvshow.nfo"), showNFO(metadataValue), force); err != nil {
			errorsFound = append(errorsFound, err.Error())
		}
	}
	if appconfig.Bool(s.Config.Snapshot(), "bangumiIniEnabled") && ani.BGMURL != "" {
		_ = writeText(filepath.Join(path, "bangumi.ini"), "[Bangumi]\nid="+subjectID(ani.BGMURL)+"\noffset="+strconv.Itoa(ani.Offset)+"\n", force)
	}
	return processed, errorsFound
}

func (s *Service) moveCompleted(path string, ani model.Ani) (string, error) {
	if ani.OVA || !ani.Completed || ani.Enable || ani.TotalEpisodeNumber < 1 || ani.CurrentEpisodeNumber < ani.TotalEpisodeNumber {
		return "", nil
	}
	if s.Config == nil || !appconfig.Bool(s.Config.Snapshot(), "autoDisabled") || !appconfig.Bool(s.Config.Snapshot(), "completed") {
		return "", nil
	}
	template := appconfig.String(s.Config.Snapshot(), "completedPathTemplate")
	if ani.CustomCompleted && strings.TrimSpace(ani.CustomCompletedPathTemplate) != "" {
		template = ani.CustomCompletedPathTemplate
	}
	if strings.TrimSpace(template) == "" || s.ResolveOther == nil {
		return "", nil
	}
	target, err := s.ResolveOther(ani, template)
	if err != nil {
		return "", err
	}
	if filepath.Clean(target) == filepath.Clean(path) {
		return "", nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		source := filepath.Join(path, entry.Name())
		destination := filepath.Join(target, entry.Name())
		if _, statErr := os.Stat(destination); statErr == nil {
			continue
		}
		if err := os.Rename(source, destination); err != nil {
			return "", fmt.Errorf("完结迁移 %s: %w", entry.Name(), err)
		}
	}
	_ = os.Remove(path)
	return target, nil
}

func (s *Service) videoFiles(path string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(path, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && isVideo(entry.Name()) {
			files = append(files, filePath)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func (s *Service) writeNFO(directory, videoPath string, ani model.Ani, value model.Metadata, identity mediaIdentity, force bool) error {
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	if ani.OVA {
		return writeXML(filepath.Join(directory, base+".nfo"), movieNFO(value), force)
	}
	if err := writeXML(filepath.Join(directory, base+".nfo"), episodeNFO(value, identity, ani.Season), force); err != nil {
		return err
	}
	if err := writeXML(filepath.Join(directory, "season.nfo"), seasonNFO(value, ani.Season), force); err != nil {
		return err
	}
	return nil
}

func (s *Service) writeShowAssets(path string, ani model.Ani, value model.Metadata, force bool) error {
	if value.Poster == "" && value.Backdrop == "" {
		return nil
	}
	if value.Poster != "" {
		if err := s.saveImage(value.Poster, filepath.Join(path, "poster"+imageExt(value.Poster)), force); err != nil {
			return err
		}
	}
	if value.Backdrop != "" {
		if err := s.saveImage(value.Backdrop, filepath.Join(path, "fanart"+imageExt(value.Backdrop)), force); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) saveImage(imagePath, target string, force bool) error {
	if !force {
		if _, err := os.Stat(target); err == nil {
			return nil
		}
	}
	imageURL := s.imageURL(imagePath)
	parsed, err := url.Parse(imageURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("图片地址格式异常")
	}
	response, err := s.HTTPClient.Get(imageURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("图片服务返回 HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return writeStream(target, response.Body)
}

func (s *Service) imageURL(value string) string {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	base := "https://image.tmdb.org"
	if s.Config != nil && appconfig.String(s.Config.Snapshot(), "tmdbImage") != "" {
		base = strings.TrimRight(appconfig.String(s.Config.Snapshot(), "tmdbImage"), "/")
	}
	return base + "/t/p/original" + value
}

func buildFilename(ani model.Ani, value model.Metadata, original string, identity mediaIdentity, config appconfig.Reader) string {
	template := "${title} S${seasonFormat}E${episodeFormat}"
	if ani.CustomRenameTemplateEnable && strings.TrimSpace(ani.CustomRenameTemplate) != "" {
		template = ani.CustomRenameTemplate
	} else if config != nil {
		aniCustom := strings.TrimSpace(appconfig.String(config.Snapshot(), "renameTemplate"))
		if aniCustom != "" {
			template = aniCustom
		}
	}
	episode := formatEpisode(identity.Episode)
	replacements := map[string]string{
		"${title}": ani.Title, "${themoviedbName}": ani.TheMovieDBName, "${tmdbid}": value.ID,
		"${seasonFormat}": fmt.Sprintf("%02d", ani.Season), "${episodeFormat}": episode,
		"${season}": strconv.Itoa(ani.Season), "${episode}": trimEpisode(identity.Episode),
		"${subgroup}": defaultValue(ani.Subgroup, "未知字幕组"), "${itemTitle}": original,
		"${resolution}": resolution(original), "${jpTitle}": ani.JPTitle, "${bgmId}": subjectID(ani.BGMURL),
		"${episodeTitle}": episodeTitle(value, identity.Episode),
		"${language}":     language(original),
	}
	for key, replacement := range replacements {
		template = strings.ReplaceAll(template, key, replacement)
	}
	result := sanitizeName(template)
	if config != nil {
		cfg := config.Snapshot()
		if appconfig.Bool(cfg, "renameDelYear") {
			result = strings.TrimSpace(regexp.MustCompile(`\s*[\(\[]?\d{4}[\)\]]?`).ReplaceAllString(result, ""))
		}
		if appconfig.Bool(cfg, "renameDelTmdbId") {
			result = strings.TrimSpace(regexp.MustCompile(`\s*(?:\[tmdbid=\d+\]|\{tmdb-\d+\})`).ReplaceAllString(result, ""))
		}
		if limit := appconfig.Int(cfg, "maxFileNameLength"); limit > 0 {
			result = truncateRunes(result, limit)
		}
	}
	return result
}

func parseIdentity(name string) mediaIdentity {
	if match := seasonEpisodePattern.FindStringSubmatch(name); len(match) > 2 {
		season, _ := strconv.Atoi(match[1])
		episode, _ := strconv.ParseFloat(match[2], 64)
		return mediaIdentity{Season: season, Episode: episode, HasEp: true}
	}
	for _, pattern := range []*regexp.Regexp{episodePattern, bracketEpisode} {
		if match := pattern.FindStringSubmatch(name); len(match) > 1 {
			episode, _ := strconv.ParseFloat(match[1], 64)
			return mediaIdentity{Episode: episode, HasEp: true}
		}
	}
	return mediaIdentity{}
}

func parseIdentityFor(name string, ani model.Ani) mediaIdentity {
	if ani.CustomEpisode {
		if episode, ok := customEpisode(name, ani.CustomEpisodeStr, ani.CustomEpisodeGroupIndex); ok {
			return mediaIdentity{Episode: episode, HasEp: true}
		}
	}
	return parseIdentity(name)
}

func customEpisode(name, expression string, group int) (float64, bool) {
	if strings.TrimSpace(expression) == "" || group < 1 {
		return 0, false
	}
	pattern, err := regexp.Compile(strings.ReplaceAll(expression, "(?:", "("))
	if err != nil {
		return 0, false
	}
	matches := pattern.FindStringSubmatch(name)
	if group >= len(matches) {
		return 0, false
	}
	number := regexp.MustCompile(`[0-9]+([.]5)?`).FindString(matches[group])
	if number == "" {
		return 0, false
	}
	episode, err := strconv.ParseFloat(number, 64)
	return episode, err == nil
}

func subtitlesFor(videoPath string) []model.SubtitleInfo {
	directory, stem := filepath.Dir(videoPath), strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, _ := os.ReadDir(directory)
	result := []model.SubtitleInfo{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !isSubtitle(entry.Name()) {
			continue
		}
		candidateStem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !strings.HasPrefix(candidateStem, stem) {
			continue
		}
		name := candidateStem
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, model.SubtitleInfo{Name: name, HTML: strings.ToUpper(name), URL: filepath.Join(directory, entry.Name()), Type: strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), "."))})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func moveSidecars(source, target string, errorsFound *[]string) {
	directory := filepath.Dir(source)
	oldStem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	newStem := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if entry.IsDir() || !isSubtitle(entry.Name()) {
			continue
		}
		if !strings.HasPrefix(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), oldStem) {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), oldStem)
		destination := filepath.Join(directory, newStem+suffix+filepath.Ext(entry.Name()))
		if _, err := os.Stat(destination); err == nil {
			continue
		}
		if err := os.Rename(filepath.Join(directory, entry.Name()), destination); err != nil {
			*errorsFound = append(*errorsFound, fmt.Sprintf("移动字幕 %s: %v", entry.Name(), err))
		}
	}
}

func isVideo(name string) bool {
	return hasExt(name, map[string]bool{"mkv": true, "mp4": true, "avi": true, "wmv": true})
}
func isSubtitle(name string) bool {
	return hasExt(name, map[string]bool{"ass": true, "ssa": true, "sub": true, "srt": true, "lyc": true, "sup": true, "pgs": true, "mks": true})
}
func hasExt(name string, values map[string]bool) bool {
	return values[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]
}

func formatEpisode(value float64) string {
	if value != float64(int(value)) {
		return fmt.Sprintf("%02d.5", int(value))
	}
	return fmt.Sprintf("%02d", int(value))
}
func trimEpisode(value float64) string {
	if value != float64(int(value)) {
		return fmt.Sprintf("%d.5", int(value))
	}
	return strconv.Itoa(int(value))
}
func episodeTitle(value model.Metadata, episode float64) string {
	for _, item := range value.EpisodeInfo {
		if float64(item.Number) == episode && item.Name != "" {
			return item.Name
		}
	}
	return "第" + trimEpisode(episode) + "集"
}
func resolution(value string) string {
	value = strings.ToLower(value)
	for _, candidate := range []string{"2160p", "1080p", "720p"} {
		if strings.Contains(value, candidate) {
			return candidate
		}
	}
	return "none"
}
func sanitizeName(value string) string {
	replacer := strings.NewReplacer("/", " ", "\\", " ", ":", "：", "?", "？", "|", "｜", "*", " ", "<", " ", ">", " ", `"`, " ", "\x00", " ")
	value = strings.TrimSpace(replacer.Replace(value))
	for strings.Contains(value, "  ") {
		value = strings.ReplaceAll(value, "  ", " ")
	}
	return value
}
func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func language(value string) string {
	lower := strings.ToLower(value)
	for _, candidate := range []string{"chs", "cht", "sc", "tc", "jpn", "eng", "中文", "简", "繁"} {
		if strings.Contains(lower, candidate) {
			return candidate
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
func FinalTitle(value model.Metadata, config appconfig.Reader) string {
	title := value.Title
	if config != nil && appconfig.Bool(config.Snapshot(), "tmdbOriginalName") && value.OriginalTitle != "" {
		title = value.OriginalTitle
	}
	if config != nil && appconfig.Bool(config.Snapshot(), "titleYear") && value.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, value.Year)
	}
	if config != nil && appconfig.Bool(config.Snapshot(), "tmdbId") && value.ID != "" {
		if appconfig.Bool(config.Snapshot(), "tmdbIdPlexMode") {
			title += " {tmdb-" + value.ID + "}"
		} else {
			title += " [tmdbid=" + value.ID + "]"
		}
	}
	return sanitizeName(title)
}
func imageExt(value string) string {
	ext := strings.ToLower(filepath.Ext(value))
	if ext == "" || len(ext) > 8 {
		return ".jpg"
	}
	return ext
}
func subjectID(value string) string {
	parsed, _ := url.Parse(value)
	if parsed != nil {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i := range parts {
			if parts[i] == "subject" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
		if result := parsed.Query().Get("subject"); result != "" {
			return result
		}
	}
	return ""
}
func writeXML(path string, value any, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	data, err := xml.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(path, append([]byte(xml.Header), data...))
}
func writeText(path, value string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	return writeBytes(path, []byte(value))
}
func writeBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".media-*.tmp")
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
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	ok = true
	return nil
}
func writeStream(path string, reader io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".image-*.tmp")
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
	if _, err := io.Copy(temp, io.LimitReader(reader, 64<<20)); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

type showNFOXML struct {
	XMLName  xml.Name `xml:"tvshow"`
	ID       string   `xml:"tmdbid,omitempty"`
	Title    string   `xml:"title,omitempty"`
	Original string   `xml:"originaltitle,omitempty"`
	Year     int      `xml:"year,omitempty"`
	Plot     string   `xml:"plot,omitempty"`
	Rating   float64  `xml:"rating,omitempty"`
	Poster   string   `xml:"thumb,omitempty"`
	Genres   []string `xml:"genre,omitempty"`
}
type seasonNFOXML struct {
	XMLName xml.Name `xml:"season"`
	Title   string   `xml:"title,omitempty"`
	Plot    string   `xml:"plot,omitempty"`
	Season  int      `xml:"seasonnumber"`
}
type episodeNFOXML struct {
	XMLName xml.Name `xml:"episodedetails"`
	Title   string   `xml:"title,omitempty"`
	Plot    string   `xml:"plot,omitempty"`
	Rating  float64  `xml:"rating,omitempty"`
	Aired   string   `xml:"aired,omitempty"`
	Episode int      `xml:"episode"`
	Season  int      `xml:"season"`
}
type movieNFOXML struct {
	XMLName  xml.Name `xml:"movie"`
	ID       string   `xml:"tmdbid,omitempty"`
	Title    string   `xml:"title,omitempty"`
	Original string   `xml:"originaltitle,omitempty"`
	Year     int      `xml:"year,omitempty"`
	Plot     string   `xml:"plot,omitempty"`
	Rating   float64  `xml:"rating,omitempty"`
	Genres   []string `xml:"genre,omitempty"`
}

func showNFO(v model.Metadata) showNFOXML {
	return showNFOXML{ID: v.ID, Title: v.Title, Original: v.OriginalTitle, Year: v.Year, Plot: v.Overview, Rating: v.Score, Genres: v.Genres}
}
func seasonNFO(v model.Metadata, season int) seasonNFOXML {
	return seasonNFOXML{Title: v.Title, Plot: v.Overview, Season: season}
}
func episodeNFO(v model.Metadata, id mediaIdentity, season int) episodeNFOXML {
	for _, e := range v.EpisodeInfo {
		if e.Number == int(id.Episode) {
			return episodeNFOXML{Title: e.Name, Plot: e.Overview, Aired: e.AirDate, Episode: e.Number, Season: season, Rating: v.Score}
		}
	}
	return episodeNFOXML{Title: "第" + trimEpisode(id.Episode) + "集", Episode: int(id.Episode), Season: season, Rating: v.Score}
}
func movieNFO(v model.Metadata) movieNFOXML {
	return movieNFOXML{ID: v.ID, Title: v.Title, Original: v.OriginalTitle, Year: v.Year, Plot: v.Overview, Rating: v.Score, Genres: v.Genres}
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
