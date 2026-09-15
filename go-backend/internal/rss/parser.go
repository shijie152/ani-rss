// Package rss parses resource feeds at the boundary, keeping source-specific
// XML quirks out of matching and downloader code.
package rss

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

type feed struct {
	Channel channel `xml:"channel"`
}
type channel struct {
	Items []entry `xml:"item"`
}
type entry struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	GUID        string    `xml:"guid"`
	Description string    `xml:"description"`
	PubDate     string    `xml:"pubDate"`
	Enclosure   enclosure `xml:"enclosure"`
	InfoHash    string    `xml:"infoHash"`
	Size        string    `xml:"size"`
	NyaaSize    string    `xml:"{http://nyaa.si/xml/}size"`
	Torrent     torrent   `xml:"torrent"`
}
type enclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
}

type torrent struct {
	InfoHash      string `xml:"infohash"`
	PubDate       string `xml:"pubDate"`
	ContentLength string `xml:"contentLength"`
	MagnetURI     string `xml:"magneturi"`
}

// Fetch downloads an RSS document using the caller's HTTP policy. It is used
// by conversion as well as the refresh coordinator so default conversion
// behavior can inspect the same feed without coupling backend handlers to
// source-specific HTTP details.
func Fetch(ctx context.Context, client *http.Client, target string, retries int) ([]byte, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("RSS地址不能为空")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if retries < 1 {
		retries = 1
	}
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", "ani-rss-go")
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<20))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
				return body, nil
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("RSS服务返回 HTTP %d", response.StatusCode)
			}
		} else {
			lastErr = err
		}
		if attempt+1 < retries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
			}
		}
	}
	return nil, lastErr
}

func Parse(data []byte, subgroup, source string) ([]model.Resource, error) {
	var document feed
	if err := xml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse RSS XML: %w", err)
	}
	resources := make([]model.Resource, 0, len(document.Channel.Items))
	for _, item := range document.Channel.Items {
		download := strings.TrimSpace(item.Enclosure.URL)
		if download == "" {
			download = strings.TrimSpace(item.Link)
		}
		if download == "" {
			download = strings.TrimSpace(item.Torrent.MagnetURI)
		}
		if download == "" {
			continue
		}
		resource := model.Resource{Title: strings.TrimSpace(item.Title), DownloadURL: download, TorrentURL: download, Description: strings.TrimSpace(item.Description), Source: source, Subgroup: subgroup, Master: true}
		resource.Episode = Episode(resource.Title)
		resource.InfoHash = strings.ToLower(strings.TrimSpace(item.InfoHash))
		if resource.InfoHash == "" {
			resource.InfoHash = strings.ToLower(strings.TrimSpace(item.Torrent.InfoHash))
		}
		if resource.InfoHash == "" {
			resource.InfoHash = infoHash(download, item.GUID)
		}
		resource.InfoHash = strings.ToLower(resource.InfoHash)
		if resource.Size, _ = strconv.ParseInt(strings.TrimSpace(item.Enclosure.Length), 10, 64); resource.Size == 0 {
			resource.Size, _ = strconv.ParseInt(strings.TrimSpace(item.Size), 10, 64)
		}
		if resource.Size == 0 {
			resource.Size, _ = strconv.ParseInt(strings.TrimSpace(item.Torrent.ContentLength), 10, 64)
		}
		if item.NyaaSize != "" {
			resource.FormatSize = item.NyaaSize
		}
		if resource.FormatSize == "" && resource.Size > 0 {
			resource.FormatSize = formatSize(resource.Size)
		}
		pubDate := strings.TrimSpace(item.PubDate)
		if pubDate == "" {
			pubDate = strings.TrimSpace(item.Torrent.PubDate)
		}
		if value, err := httpDate(pubDate); err == nil {
			resource.PublishedAt = &value
		}
		if strings.HasPrefix(strings.ToLower(download), "magnet:") {
			resource.Magnet = download
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

var episodePatterns = []*regexp.Regexp{regexp.MustCompile(`(?i)(?:^|[^a-z])(?:e|ep|episode|第)\s*([0-9]+(?:\.5)?)(?:[^0-9]|$)`), regexp.MustCompile(`(?:\[|\s|-)\s*([0-9]+(?:\.5)?)\s*(?:\]|(?:\s|$))`)}

func Episode(title string) float64 {
	for _, pattern := range episodePatterns {
		if match := pattern.FindStringSubmatch(title); len(match) > 1 {
			value, _ := strconv.ParseFloat(match[1], 64)
			return value
		}
	}
	return 0
}
func infoHash(download, guid string) string {
	candidate := strings.TrimSpace(guid)
	if candidate == "" {
		if parsed, err := url.Parse(download); err == nil {
			candidate = parsed.Path
		}
		candidate = strings.Trim(candidate, "/")
	}
	candidate = strings.TrimSpace(candidate)
	if strings.Contains(strings.ToLower(download), "btih:") {
		lower := strings.ToLower(download)
		start := strings.Index(lower, "btih:") + 5
		value := download[start:]
		if end := strings.IndexAny(value, `&"'<>`); end >= 0 {
			value = value[:end]
		}
		if value = strings.TrimSpace(value); value != "" {
			candidate = value
		}
	}
	return candidate
}
func httpDate(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC822} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}
func formatSize(size int64) string { return fmt.Sprintf("%.2f MiB", float64(size)/(1024*1024)) }
