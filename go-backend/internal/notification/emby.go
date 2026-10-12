package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/outbound"
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
	response, err := client.Do(ctx, outbound.Call{
		Service:  "bangumi",
		Method:   http.MethodPost,
		URL:      base + "/v0/episodes",
		Header:   http.Header{"Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/x-www-form-urlencoded"}},
		Body:     []byte(form.Encode()),
		MaxBytes: 4 << 20,
	})
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("Bangumi episodes HTTP %d", response.Status)
	}
	var episodes struct {
		Data []struct {
			ID   string  `json:"id"`
			Ep   float64 `json:"ep"`
			Sort float64 `json:"sort"`
		} `json:"data"`
	}
	if err := response.JSON(&episodes); err != nil {
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
	currentResponse, err := client.Do(ctx, outbound.Call{
		Service:  "bangumi",
		Method:   http.MethodGet,
		URL:      base + "/v0/users/-/collections/-/episodes/" + url.PathEscape(episodeID),
		Header:   http.Header{"Authorization": []string{"Bearer " + token}},
		MaxBytes: 1 << 20,
	})
	if err != nil {
		return err
	}
	var current struct {
		Type int `json:"type"`
	}
	if currentResponse.OK() {
		_ = currentResponse.JSON(&current)
	}
	if current.Type == status {
		return nil
	}
	body, _ := json.Marshal(map[string]int{"type": status})
	updated, err := client.Do(ctx, outbound.Call{
		Service: "bangumi",
		Method:  http.MethodPut,
		URL:     base + "/v0/users/-/collections/-/episodes/" + url.PathEscape(episodeID),
		Header:  http.Header{"Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/json"}},
		Body:    body,
	})
	if err != nil {
		return err
	}
	if !updated.OK() {
		return fmt.Errorf("Bangumi episode update HTTP %d", updated.Status)
	}
	return nil
}

var embySeasonEpisodePattern = regexp.MustCompile(`(?i)s(\d{1,3})[ ._-]*e(\d+(?:\.5)?)`)
