package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/httpclient"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/source"
)

const defaultBGMTokenStatusURL = "https://bgm.tv/oauth/token_status"
const defaultBGMOAuthURL = "https://bgm.tv/oauth/access_token"

func (a *App) bgmClient() (*http.Client, model.Config, error) {
	cfg := a.config.Snapshot()
	client, err := httpclient.New(cfg, time.Duration(appconfig.Int(cfg, "rssTimeout"))*time.Second)
	if err != nil {
		return nil, nil, err
	}
	return client, cfg, nil
}

func bgmAPIBase(cfg model.Config) string {
	base := strings.TrimRight(appconfig.String(cfg, "bgmApi"), "/")
	if base == "" {
		return "https://api.bgm.tv"
	}
	return base
}

func bgmToken(cfg model.Config) (string, error) {
	token := appconfig.String(cfg, "bgmToken")
	if token == "" {
		return "", errors.New("BgmToken 未填写")
	}
	return token, nil
}

func bgmRequest(ctx context.Context, client *http.Client, method, target, token string, body []byte, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return client.Do(request)
}

func bgmJSONResponse(response *http.Response, target any) error {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Bangumi HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(target); err != nil {
		return err
	}
	return nil
}

func (a *App) bgmMeRaw(ctx context.Context, client *http.Client, cfg model.Config, token string) (map[string]any, error) {
	response, err := bgmRequest(ctx, client, http.MethodGet, bgmAPIBase(cfg)+"/v0/me", token, nil, "")
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := bgmJSONResponse(response, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func bgmUsername(value map[string]any) string {
	if username := bgmString(value, "username"); username != "" {
		return username
	}
	if id := bgmString(value, "id"); id != "" {
		return id
	}
	return ""
}

func (a *App) rate(w http.ResponseWriter, r *http.Request) {
	var item model.Ani
	if err := decodeJSON(r, &item); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	subjectID := source.SubjectID(item.BGMURL)
	if subjectID == "" {
		writeResult(w, http.StatusInternalServerError, nil, "bgmUrl 不能为空")
		return
	}
	client, cfg, err := a.bgmClient()
	if err == nil {
		var token string
		token, err = bgmToken(cfg)
		if err == nil {
			var me map[string]any
			me, err = a.bgmMeRaw(r.Context(), client, cfg, token)
			if err == nil {
				username := bgmUsername(me)
				if username == "" {
					err = errors.New("BGM 用户信息缺少 username/id")
				} else {
					var response *http.Response
					response, err = bgmRequest(r.Context(), client, http.MethodGet, bgmAPIBase(cfg)+"/v0/users/"+url.PathEscape(username)+"/collections/"+url.PathEscape(subjectID), token, nil, "")
					if err == nil && response.StatusCode == http.StatusNotFound {
						response.Body.Close()
						writeResult(w, http.StatusOK, 0, "success")
						return
					}
					if err == nil {
						var collection struct {
							Rate int `json:"rate"`
						}
						if err = bgmJSONResponse(response, &collection); err == nil {
							writeResult(w, http.StatusOK, collection.Rate, "success")
							return
						}
					}
				}
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) setRate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BGMURL string   `json:"bgmUrl"`
		Score  *float64 `json:"score"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeResult(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	subjectID := source.SubjectID(input.BGMURL)
	if subjectID == "" {
		writeResult(w, http.StatusInternalServerError, nil, "bgmUrl 不能为空")
		return
	}
	if input.Score == nil {
		// Java treats a missing/null score as a read operation, even though the
		// endpoint still returns its fixed save-success message.
		a.rateWithMessage(w, r, subjectID, "保存评分成功")
		return
	}
	score := int(*input.Score)
	client, cfg, err := a.bgmClient()
	if err == nil {
		var token string
		token, err = bgmToken(cfg)
		if err == nil {
			body, marshalErr := json.Marshal(map[string]int{"type": 3, "rate": score})
			if marshalErr != nil {
				err = marshalErr
			} else {
				var response *http.Response
				response, err = bgmRequest(r.Context(), client, http.MethodPost, bgmAPIBase(cfg)+"/v0/users/-/collections/"+url.PathEscape(subjectID), token, body, "application/json")
				if err == nil {
					if response.StatusCode >= 200 && response.StatusCode < 300 {
						response.Body.Close()
						writeResult(w, http.StatusOK, score, "保存评分成功")
						return
					}
					err = fmt.Errorf("Bangumi HTTP %d", response.StatusCode)
					response.Body.Close()
				}
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) rateWithMessage(w http.ResponseWriter, r *http.Request, subjectID, message string) {
	// Keep the read path in one place so setRate's missing-score behavior is
	// byte-for-byte equivalent to the legacy controller's observable result.
	client, cfg, err := a.bgmClient()
	if err == nil {
		var token string
		token, err = bgmToken(cfg)
		if err == nil {
			var me map[string]any
			me, err = a.bgmMeRaw(r.Context(), client, cfg, token)
			if err == nil {
				username := bgmUsername(me)
				if username == "" {
					err = errors.New("BGM 用户信息缺少 username/id")
				} else {
					var response *http.Response
					response, err = bgmRequest(r.Context(), client, http.MethodGet, bgmAPIBase(cfg)+"/v0/users/"+url.PathEscape(username)+"/collections/"+url.PathEscape(subjectID), token, nil, "")
					if err == nil && response.StatusCode == http.StatusNotFound {
						response.Body.Close()
						writeResult(w, http.StatusOK, 0, message)
						return
					}
					if err == nil {
						var collection struct {
							Rate int `json:"rate"`
						}
						if err = bgmJSONResponse(response, &collection); err == nil {
							writeResult(w, http.StatusOK, collection.Rate, message)
							return
						}
					}
				}
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) meBGM(w http.ResponseWriter, r *http.Request) {
	client, cfg, err := a.bgmClient()
	if err == nil {
		var token string
		token, err = bgmToken(cfg)
		if err == nil {
			var expiresDays int
			expiresDays, err = a.bgmExpiresDays(r.Context(), client, cfg, token)
			if err == nil {
				var raw map[string]any
				raw, err = a.bgmMeRaw(r.Context(), client, cfg, token)
				if err == nil {
					writeResult(w, http.StatusOK, normalizeBGMMe(raw, expiresDays), "success")
					return
				}
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}

func (a *App) bgmExpiresDays(ctx context.Context, client *http.Client, cfg model.Config, token string) (int, error) {
	target := defaultBGMTokenStatusURL
	if configured := appconfig.String(cfg, "bgmTokenStatusApi"); configured != "" {
		target = strings.TrimRight(configured, "/")
	}
	form := url.Values{"access_token": []string{token}}
	response, err := bgmRequest(ctx, client, http.MethodPost, target, "", []byte(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return 0, err
	}
	var value struct {
		Expires int64 `json:"expires"`
	}
	if err := bgmJSONResponse(response, &value); err != nil {
		return 0, err
	}
	remaining := time.Unix(value.Expires, 0).Sub(time.Now())
	if remaining <= 0 {
		return 0, nil
	}
	return int(remaining / (24 * time.Hour)), nil
}

func normalizeBGMMe(raw map[string]any, expiresDays int) map[string]any {
	result := map[string]any{"expiresDays": expiresDays}
	copyBGMField(result, raw, "id", "id")
	copyBGMField(result, raw, "sign", "sign")
	copyBGMField(result, raw, "url", "url")
	copyBGMField(result, raw, "username", "username")
	copyBGMField(result, raw, "nickname", "nickname")
	if value := bgmString(raw, "userGroup", "user_group"); value != "" {
		result["userGroup"] = value
	}
	if value := bgmDateString(raw, "regTime", "reg_time"); value != "" {
		result["regTime"] = value
	}
	copyBGMField(result, raw, "email", "email")
	if value := bgmInt(raw, "timeOffset", "time_offset"); value != nil {
		result["timeOffset"] = *value
	}
	if avatar, ok := raw["avatar"].(map[string]any); ok {
		result["avatar"] = avatar
	}
	return result
}

func copyBGMField(result, raw map[string]any, output, input string) {
	if value, ok := raw[input]; ok && value != nil {
		result[output] = value
	}
}

func bgmString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if item, ok := value[key]; ok && item != nil {
			switch typed := item.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case json.Number:
				return typed.String()
			case float64:
				return strconv.FormatFloat(typed, 'f', -1, 64)
			}
		}
	}
	return ""
}

func bgmInt(value map[string]any, keys ...string) *int {
	for _, key := range keys {
		if item, ok := value[key]; ok && item != nil {
			parsed, err := strconv.Atoi(bgmString(map[string]any{"value": item}, "value"))
			if err == nil {
				return &parsed
			}
		}
	}
	return nil
}

func bgmDateString(value map[string]any, keys ...string) string {
	raw := bgmString(value, keys...)
	if raw == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format("2006-01-02 15:04:05")
		}
	}
	return raw
}

func (a *App) bgmOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code, queryErr := requiredQuery(r, "code")
	if queryErr != nil {
		writeResult(w, http.StatusInternalServerError, nil, queryErr.Error())
		return
	}
	code = strings.TrimSpace(code)
	client, cfg, err := a.bgmClient()
	if err == nil {
		form := url.Values{
			"grant_type":    []string{"authorization_code"},
			"client_id":     []string{appconfig.String(cfg, "bgmAppID")},
			"client_secret": []string{appconfig.String(cfg, "bgmAppSecret")},
			"code":          []string{code},
			"redirect_uri":  []string{appconfig.String(cfg, "bgmRedirectUri")},
		}
		{
			target := defaultBGMOAuthURL
			if configured := appconfig.String(cfg, "bgmOAuthApi"); configured != "" {
				target = strings.TrimRight(configured, "/")
			}
			var response *http.Response
			response, err = bgmRequest(r.Context(), client, http.MethodPost, target, "", []byte(form.Encode()), "application/x-www-form-urlencoded")
			if err == nil {
				var value struct {
					AccessToken  string `json:"access_token"`
					RefreshToken string `json:"refresh_token"`
				}
				if decodeErr := bgmJSONResponse(response, &value); decodeErr != nil {
					err = decodeErr
				} else if value.AccessToken == "" || value.RefreshToken == "" {
					err = errors.New("OAuth 返回缺少 token")
				} else if updateErr := a.config.Update(model.Config{"bgmToken": value.AccessToken, "bgmRefreshToken": value.RefreshToken}); updateErr != nil {
					err = updateErr
				} else {
					writeResult(w, http.StatusOK, nil, "授权成功, 现在你可以关闭此窗口")
					return
				}
			}
		}
	}
	writeResult(w, http.StatusInternalServerError, nil, sourceError(err))
}
