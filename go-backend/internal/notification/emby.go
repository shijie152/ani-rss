package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
)

// ProcessEmbyWebhook applies the Emby playback/mark-played webhook to the
// Bangumi collection, mirroring the Java EmbyWebhook behavior. It is the
// notification-domain side effect of the Emby HTTP route, kept out of the
// route shell so app.go stays a thin handler.
//
// subscriptions supplies the candidate subscriptions to match against; it is
// a func rather than the subscription service so this package does not depend
// on the subscription module. The handler passes a.subscriptions.Items.
func (d *Dispatcher) ProcessEmbyWebhook(ctx context.Context, payload map[string]any, subscriptions func() []model.Ani) error {
	cfg := d.Config.Snapshot()
	token := appconfig.String(cfg, "bgmToken")
	if token == "" {
		return nil
	}
	event := strings.ToLower(appconfig.String(payload, "event"))
	if event == "system.webhooktest" || event == "system.notificationtest" {
		return nil
	}
	item, _ := payload["Item"].(map[string]any)
	if item == nil {
		item, _ = payload["item"].(map[string]any)
	}
	fileName, seriesName := appconfig.String(item, "FileName"), appconfig.String(item, "SeriesName")
	if fileName == "" {
		fileName = appconfig.String(item, "fileName")
	}
	if seriesName == "" {
		seriesName = appconfig.String(item, "seriesName")
	}
	match := embySeasonEpisodePattern.FindStringSubmatch(fileName)
	if len(match) < 3 {
		return nil
	}
	season, _ := strconv.Atoi(match[1])
	episode, _ := strconv.ParseFloat(match[2], 64)
	if season < 1 || episode != float64(int(episode)) {
		return nil
	}
	playback, _ := payload["PlaybackInfo"].(map[string]any)
	if playback == nil {
		playback, _ = payload["playbackInfo"].(map[string]any)
	}
	status := -1
	switch event {
	case "item.markunplayed":
		status = 0
	case "item.markplayed":
		status = 2
	case "playback.stop":
		if played, ok := playback["PlayedToCompletion"].(bool); ok && played {
			status = 2
		}
		if played, ok := playback["playedToCompletion"].(bool); ok && played {
			status = 2
		}
	}
	if status < 0 {
		return nil
	}
	var selected model.Ani
	found := false
	for _, candidate := range subscriptions() {
		if candidate.Season != season || candidate.BGMURL == "" {
			continue
		}
		if candidate.Title == seriesName || candidate.TheMovieDBName == seriesName {
			selected, found = candidate, true
			break
		}
	}
	if !found {
		return nil
	}
	subjectID := source.SubjectID(selected.BGMURL)
	if subjectID == "" {
		return nil
	}
	client := d.Client
	base := strings.TrimRight(appconfig.String(cfg, "bgmApi"), "/")
	if base == "" {
		base = "https://api.bgm.tv"
	}
	form := url.Values{"subject_id": []string{subjectID}, "type": []string{"0"}, "limit": []string{"1000"}, "offset": []string{"0"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v0/episodes", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Bangumi episodes HTTP %d", response.StatusCode)
	}
	var episodes struct {
		Data []struct {
			ID   string  `json:"id"`
			Ep   float64 `json:"ep"`
			Sort float64 `json:"sort"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&episodes); err != nil {
		return err
	}
	episodeID := ""
	for _, candidate := range episodes.Data {
		if candidate.Ep == episode || candidate.Sort == episode {
			episodeID = candidate.ID
			if candidate.Ep == episode {
				break
			}
		}
	}
	if episodeID == "" {
		return nil
	}
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v0/users/-/collections/-/episodes/"+url.PathEscape(episodeID), nil)
	if err != nil {
		return err
	}
	get.Header.Set("Authorization", "Bearer "+token)
	currentResponse, err := client.Do(get)
	if err != nil {
		return err
	}
	defer currentResponse.Body.Close()
	var current struct {
		Type int `json:"type"`
	}
	if currentResponse.StatusCode >= 200 && currentResponse.StatusCode < 300 {
		_ = json.NewDecoder(io.LimitReader(currentResponse.Body, 1<<20)).Decode(&current)
	}
	if current.Type == status {
		return nil
	}
	body, _ := json.Marshal(map[string]int{"type": status})
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/v0/users/-/collections/-/episodes/"+url.PathEscape(episodeID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	put.Header.Set("Authorization", "Bearer "+token)
	put.Header.Set("Content-Type", "application/json")
	updated, err := client.Do(put)
	if err != nil {
		return err
	}
	defer updated.Body.Close()
	if updated.StatusCode < 200 || updated.StatusCode >= 300 {
		return fmt.Errorf("Bangumi episode update HTTP %d", updated.StatusCode)
	}
	return nil
}

var embySeasonEpisodePattern = regexp.MustCompile(`(?i)s(\d{1,3})[ ._-]*e(\d+(?:\.5)?)`)
