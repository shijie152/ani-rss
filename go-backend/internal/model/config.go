package model

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func DefaultConfig() Config {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	api := make([]byte, 48)
	_, _ = rand.Read(api)
	root := "/Media"
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		root = filepath.Join(home, "Movies")
	}
	return Config{
		"mikanHost": "https://mikanani.me", "tmdbApi": "https://api.themoviedb.org", "tmdbApiKey": "", "tmdbImage": "https://image.tmdb.org", "tmdbAnime": true,
		"downloadToolType": "qBittorrent", "downloadRetry": 3, "downloadToolHost": "", "downloadToolUsername": "", "downloadToolPassword": "", "qbUseDownloadPath": false, "qbContentLayout": "Original",
		"ratioLimit": -2, "seedingTimeLimit": -2, "inactiveSeedingTimeLimit": -2,
		"downloadPathTemplate": filepath.Join(root, "番剧", "${title}", "Season ${season}"), "ovaDownloadPathTemplate": filepath.Join(root, "剧场版", "${title}"), "completedPathTemplate": filepath.Join(root, "已完结番剧", "${title}", "Season ${season}"),
		"customTags": []any{}, "priorityKeywordsEnable": false, "priorityKeywords": []any{}, "delayedDownload": 0, "rssSleepMinutes": 15, "renameSleepSeconds": 10, "rename": true, "rss": true, "rssTimeout": 20,
		"fileExist": false, "awaitStalledUP": true, "delete": false, "deleteStandbyRSSOnly": false, "offset": false, "titleYear": true, "autoDisabled": false, "skip5": true, "standbyRss": false, "coexist": false,
		"logsMax": 128, "debug": false, "procrastinatingMasterOnly": true, "proxy": false, "proxyHost": "", "proxyPort": 8080, "proxyUsername": "", "proxyPassword": "", "downloadCount": 0,
		"login": map[string]any{"username": "admin", "password": md5Hex("admin")}, "multiLoginForbidden": true, "loginEffectiveHours": 3,
		"exclude": []any{"720[Pp]", `\d-\d`, "合集", "特别篇"}, "importExclude": false, "enabledExclude": false, "bgmJpName": false, "tmdb": true, "tmdbId": false, "tmdbIdPlexMode": false, "tmdbLanguage": "zh-CN", "tmdbRomaji": false, "tmdbOriginalName": false,
		"ipWhitelist": false, "ipWhitelistStr": "", "omit": true, "bgmTokenType": "INPUT", "bgmToken": "", "bgmAppID": "", "bgmAppSecret": "", "bgmRefreshToken": "", "bgmRedirectUri": "", "apiKey": strings.ToLower(base64.RawURLEncoding.EncodeToString(api)), "downloadNew": false, "innerIP": false, "verifyLoginIp": false,
		"renameTemplate": "[${subgroup}] ${title} S${seasonFormat}E${episodeFormat}", "renameDelYear": false, "renameDelTmdbId": false, "autoTrackersUpdate": false, "trackersUpdateUrls": "https://cf.trackerslist.com/best.txt", "notificationTemplate": "${emoji}${emoji}${emoji}\n事件类型: ${action}\n标题: ${title}", "autoUpdate": false, "version": "", "bgmImageSize": "medium", "customCss": "", "customJs": "",
		"customEpisode": false, "customEpisodeStr": `(.*|\[.*])(( - |Vol |[Ee][Pp]?)\d+(\.5)?( ?\(\d+\))?|【\d+(\.5)?】|\[\d+(\.5)?( ?\(\d+\))?( ?[vV]\d)?( ?END)?( ?完)?( ?FIN)?]|第\d+(\.5)?[话話集]( - END)?|^\[TOC].* \d+|^\[六四位元字幕组.*★\d+(\.5)?★)`, "customEpisodeGroupIndex": 2, "provider": "115 Open", "upload": true, "upLimit": int64(0), "dlLimit": int64(0), "procrastinating": false, "procrastinatingDay": 14, "githubToken": "", "updateTotalEpisodeNumber": false, "forceUpdateTotalEpisodeNumber": false, "openListDownloadTimeout": 60, "openListDownloadRetryNumber": int64(5),
		"configBackup": false, "configBackupDay": 7, "completed": false, "notificationConfigList": []any{}, "copyMasterToStandby": false, "sortType": "SCORE", "proxyList": "mikanani.me\nmikanime.tv\nanibt.net\nanimes.garden\nnyaa.si\ntmdb.org\nthemoviedb.org\nbgm.tv\nbangumi.tv\ngithub.com\nraw.githubusercontent.com", "scrape": false, "followDay": 14, "bangumiIniEnabled": false, "replace": false, "maxFileNameLength": 0, "limitLoginAttempts": true,
		"reverseProxyTrustIpListEnabled": false, "reverseProxyTrustIpList": []any{"127.0.0.1"}, "subtitleIndependentFolderEnabled": false, "subtitleIndependentFolderName": "Subs", "bgmApi": "https://api.bgm.tv", "autoStart": false, "allowCors": false, "uuid": newID(), "jwtKey": base64.StdEncoding.EncodeToString(key), "tokenId": newID(),
		"gitInfo":          map[string]any{"branch": "", "shortCommitId": "", "commitId": ""},
		"runtimeOwnership": map[string]any{"rss": "go", "rename": "go", "maintenance": "go"},
	}
}

func md5Hex(input string) string {
	// The import avoids exposing a password hashing implementation through the
	// public model package; MD5 is retained only because the current UI sends
	// this exact legacy digest at login.
	return fmt.Sprintf("%x", md5Sum([]byte(input)))
}

func md5Sum(input []byte) [16]byte {
	// Kept as a tiny wrapper so defaults and login use the same representation.
	// crypto/md5 is intentionally called in a separate file by gofmt/import
	// grouping below.
	return md5Digest(input)
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
