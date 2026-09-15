// Package model contains the JSON shapes shared by the Go business modules.
// The field names intentionally mirror the existing Java/Vue contract.
package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Config is kept open-ended on purpose. The UI sends the complete, evolving
// configuration object and the migration must not discard fields that a newer
// UI knows about before Go does.
type Config = map[string]any

type Login struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Log struct {
	TS         int64  `json:"ts"`
	Message    string `json:"message"`
	Level      string `json:"level"`
	LoggerName string `json:"loggerName"`
	ThreadName string `json:"threadName"`
}

type StandbyRSS struct {
	Label  string `json:"label,omitempty"`
	URL    string `json:"url,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type Ani struct {
	Sort                         int            `json:"sort,omitempty"`
	ID                           string         `json:"id"`
	MikanTitle                   string         `json:"mikanTitle"`
	URL                          string         `json:"url"`
	Exists                       bool           `json:"exists,omitempty"`
	StandbyRSSList               []StandbyRSS   `json:"standbyRssList"`
	Title                        string         `json:"title"`
	JPTitle                      string         `json:"jpTitle"`
	Offset                       int            `json:"offset"`
	ReleaseDate                  string         `json:"releaseDate"`
	Year                         int            `json:"year,omitempty"`
	Month                        int            `json:"month,omitempty"`
	Date                         int            `json:"date,omitempty"`
	WeekLabel                    string         `json:"weekLabel,omitempty"`
	Season                       int            `json:"season"`
	Cover                        string         `json:"cover"`
	Image                        string         `json:"image"`
	Subgroup                     string         `json:"subgroup"`
	Match                        []string       `json:"match"`
	Exclude                      []string       `json:"exclude"`
	GlobalExclude                bool           `json:"globalExclude"`
	OVA                          bool           `json:"ova"`
	Pinyin                       string         `json:"pinyin,omitempty"`
	PinyinInitials               string         `json:"pinyinInitials,omitempty"`
	Enable                       bool           `json:"enable"`
	CurrentEpisodeNumber         int            `json:"currentEpisodeNumber"`
	TotalEpisodeNumber           int            `json:"totalEpisodeNumber"`
	TheMovieDBName               string         `json:"themoviedbName"`
	Type                         string         `json:"type"`
	BGMURL                       string         `json:"bgmUrl"`
	CustomDownloadPath           bool           `json:"customDownloadPath"`
	CustomDownloadPathTemplate   string         `json:"customDownloadPathTemplate"`
	Score                        float64        `json:"score"`
	CustomEpisode                bool           `json:"customEpisode"`
	CustomEpisodeStr             string         `json:"customEpisodeStr"`
	CustomEpisodeGroupIndex      int            `json:"customEpisodeGroupIndex"`
	Omit                         bool           `json:"omit"`
	DownloadNew                  bool           `json:"downloadNew"`
	NotDownload                  []float64      `json:"notDownload"`
	TMDB                         map[string]any `json:"tmdb"`
	Upload                       bool           `json:"upload"`
	Procrastinating              bool           `json:"procrastinating"`
	CustomRenameTemplateEnable   bool           `json:"customRenameTemplateEnable"`
	CustomRenameTemplate         string         `json:"customRenameTemplate"`
	CustomPriorityKeywordsEnable bool           `json:"customPriorityKeywordsEnable"`
	CustomPriorityKeywords       []string       `json:"customPriorityKeywords"`
	LastDownloadTime             int64          `json:"lastDownloadTime"`
	CustomUploadEnable           bool           `json:"customUploadEnable"`
	CustomUploadPathTarget       string         `json:"customUploadPathTarget"`
	Message                      bool           `json:"message"`
	Completed                    bool           `json:"completed"`
	CustomCompleted              bool           `json:"customCompleted"`
	CustomCompletedPathTemplate  string         `json:"customCompletedPathTemplate"`
	CustomTagsEnable             bool           `json:"customTagsEnable"`
	CustomTags                   []string       `json:"customTags"`
	present                      map[string]bool
}

// UnmarshalJSON accepts both the current array form and the legacy UI's
// temporary string form ("[]") for match rules.
func (a *Ani) UnmarshalJSON(data []byte) error {
	type plain Ani
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	present := make(map[string]bool, len(fields))
	for key := range fields {
		present[key] = true
	}
	match := fields["match"]
	delete(fields, "match")
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var value plain
	if err := json.Unmarshal(rest, &value); err != nil {
		return err
	}
	if len(match) > 0 && string(match) != "null" {
		if err := json.Unmarshal(match, &value.Match); err != nil {
			var text string
			if textErr := json.Unmarshal(match, &text); textErr != nil {
				return fmt.Errorf("match must be an array or JSON string: %w", err)
			}
			if text == "" {
				value.Match = nil
			} else if err := json.Unmarshal([]byte(text), &value.Match); err != nil {
				return fmt.Errorf("decode match string: %w", err)
			}
		}
	}
	value.present = present
	*a = Ani(value)
	return nil
}

// FieldPresent reports whether a JSON-decoded subscription explicitly
// contained the named field. It lets startup migration distinguish an old
// record that omitted a boolean/number from a current record that explicitly
// set it to false/zero. Values created in Go code intentionally report false.
func (a Ani) FieldPresent(name string) bool { return a.present != nil && a.present[name] }

type Item struct {
	Title         string     `json:"title,omitempty"`
	ReName        string     `json:"reName,omitempty"`
	Torrent       string     `json:"torrent,omitempty"`
	InfoHash      string     `json:"infoHash,omitempty"`
	Episode       float64    `json:"episode,omitempty"`
	FormatSize    string     `json:"formatSize,omitempty"`
	Length        int64      `json:"length,omitempty"`
	HasDownloaded bool       `json:"hasDownloaded,omitempty"`
	Master        bool       `json:"master,omitempty"`
	Subgroup      string     `json:"subgroup,omitempty"`
	PubDate       *time.Time `json:"pubDate,omitempty"`
	Source        string     `json:"source,omitempty"`
	Description   string     `json:"description,omitempty"`
}

// CollectionInfo is the request shape used by the existing collection dialog.
// Filename and BGMInfo are retained even though the backend only needs the
// base64 torrent and subscription; the UI sends both fields as part of its
// form state.
type CollectionInfo struct {
	Filename string         `json:"filename,omitempty"`
	Torrent  string         `json:"torrent"`
	Ani      Ani            `json:"ani"`
	BGMInfo  map[string]any `json:"bgmInfo,omitempty"`
}

type ListAni struct {
	ReleaseDateList []string  `json:"releaseDateList"`
	WeekList        []WeekAni `json:"weekList"`
	Total           int       `json:"total"`
}

type WeekAni struct {
	WeekLabel string `json:"weekLabel"`
	Items     []Ani  `json:"items"`
}

type Torrent struct {
	ID          string   `json:"id,omitempty"`
	Hash        string   `json:"hash"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Progress    float64  `json:"progress"`
	Size        int64    `json:"size"`
	Downloaded  int64    `json:"-"`
	Completed   int64    `json:"completed"`
	FormatSize  string   `json:"formatSize"`
	SavePath    string   `json:"savePath"`
	Category    string   `json:"category"`
	Tags        []string `json:"-"`
	TagList     []string `json:"tagList"`
	AddedOn     int64    `json:"addedOn"`
	CompletedOn int64    `json:"completedOn"`
}

type Resource struct {
	AniID       string     `json:"aniId,omitempty"`
	Title       string     `json:"title"`
	Episode     float64    `json:"episode"`
	Size        int64      `json:"size"`
	FormatSize  string     `json:"formatSize"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	DownloadURL string     `json:"downloadUrl"`
	TorrentURL  string     `json:"torrent,omitempty"`
	Magnet      string     `json:"magnet,omitempty"`
	InfoHash    string     `json:"infoHash,omitempty"`
	Description string     `json:"description,omitempty"`
	Source      string     `json:"source,omitempty"`
	Subgroup    string     `json:"subgroup,omitempty"`
	Master      bool       `json:"master"`
}

type Metadata struct {
	ID            string         `json:"id,omitempty"`
	Title         string         `json:"title,omitempty"`
	OriginalTitle string         `json:"originalTitle,omitempty"`
	Overview      string         `json:"overview,omitempty"`
	Year          int            `json:"year,omitempty"`
	Season        int            `json:"season,omitempty"`
	Episodes      int            `json:"episodes,omitempty"`
	Score         float64        `json:"score,omitempty"`
	Poster        string         `json:"poster,omitempty"`
	Backdrop      string         `json:"backdrop,omitempty"`
	AirDate       string         `json:"airDate,omitempty"`
	Genres        []string       `json:"genres,omitempty"`
	EpisodeInfo   []EpisodeMeta  `json:"episodeInfo,omitempty"`
	Raw           map[string]any `json:"-"`
}

type EpisodeMeta struct {
	Number   int    `json:"number"`
	Name     string `json:"name,omitempty"`
	Overview string `json:"overview,omitempty"`
	AirDate  string `json:"airDate,omitempty"`
	Still    string `json:"still,omitempty"`
}

type MediaFile struct {
	Title      string         `json:"title"`
	Filename   string         `json:"filename"`
	Name       string         `json:"name"`
	LastModify int64          `json:"lastModify"`
	Episode    float64        `json:"episode"`
	FormatSize string         `json:"formatSize"`
	ExtName    string         `json:"extName"`
	Subtitles  []SubtitleInfo `json:"subtitles,omitempty"`
}

type SubtitleInfo struct {
	HTML    string `json:"html,omitempty"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
	Type    string `json:"type,omitempty"`
}

type RSSItem struct {
	Title       string
	Link        string
	GUID        string
	Description string
	PubDate     string
	Enclosure   string
	Length      int64
}
