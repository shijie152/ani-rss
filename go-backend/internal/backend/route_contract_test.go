package backend_test

import (
	"net/http"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/backend"
)

func TestCoreRoutesCoverTheJavaHTTPSurface(t *testing.T) {
	app, err := backend.New(backend.Options{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	expected := map[string][]string{
		"/api/about":                    {http.MethodPost},
		"/api/addAni":                   {http.MethodPost},
		"/api/aniBT":                    {http.MethodPost},
		"/api/aniBTGroup":               {http.MethodPost},
		"/api/animeGardenGroup":         {http.MethodPost},
		"/api/animeGardenList":          {http.MethodPost},
		"/api/batchEnable":              {http.MethodPost},
		"/api/batchScrape":              {http.MethodPost},
		"/api/bgm/oauth/callback":       {http.MethodPost},
		"/api/calendar.ics":             {http.MethodGet},
		"/api/clearCache":               {http.MethodPost},
		"/api/clearLogs":                {http.MethodPost},
		"/api/config":                   {http.MethodPost},
		"/api/custom.css":               {http.MethodGet},
		"/api/custom.js":                {http.MethodGet},
		"/api/deleteAni":                {http.MethodPost},
		"/api/deleteTorrent":            {http.MethodPost},
		"/api/downloadLoginTest":        {http.MethodPost},
		"/api/downloadLogs":             {http.MethodGet},
		"/api/downloadPath":             {http.MethodPost},
		"/api/embyWebHook":              {http.MethodPost},
		"/api/exportConfig":             {http.MethodGet},
		"/api/file":                     {http.MethodGet},
		"/api/getAniBySubjectId":        {http.MethodPost},
		"/api/getBgmTitle":              {http.MethodPost},
		"/api/getCollectionSubgroup":    {http.MethodPost},
		"/api/getEmbyViews":             {http.MethodPost},
		"/api/getSubtitles":             {http.MethodPost},
		"/api/getTgUpdates":             {http.MethodPost},
		"/api/getThemoviedbGroup":       {http.MethodPost},
		"/api/getThemoviedbName":        {http.MethodPost},
		"/api/importAni":                {http.MethodPost},
		"/api/importConfig":             {http.MethodPost},
		"/api/listAni":                  {http.MethodPost},
		"/api/login":                    {http.MethodPost},
		"/api/logs":                     {http.MethodPost},
		"/api/mikan":                    {http.MethodPost},
		"/api/mikanGroup":               {http.MethodPost},
		"/api/meBgm":                    {http.MethodPost},
		"/api/newNotification":          {http.MethodPost},
		"/api/ping":                     {http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions},
		"/api/playList":                 {http.MethodPost},
		"/api/previewAni":               {http.MethodPost},
		"/api/previewCollection":        {http.MethodPost},
		"/api/proxyImage":               {http.MethodGet},
		"/api/rate":                     {http.MethodPost},
		"/api/refreshAll":               {http.MethodPost},
		"/api/refreshAni":               {http.MethodPost},
		"/api/refreshCover":             {http.MethodPost},
		"/api/rssToAni":                 {http.MethodPost},
		"/api/scrape":                   {http.MethodPost},
		"/api/searchBgm":                {http.MethodPost},
		"/api/setAni":                   {http.MethodPost},
		"/api/setConfig":                {http.MethodPost},
		"/api/setRate":                  {http.MethodPost},
		"/api/startCollection":          {http.MethodPost},
		"/api/stop":                     {http.MethodPost},
		"/api/testIpWhitelist":          {http.MethodPost},
		"/api/testNotification":         {http.MethodPost},
		"/api/testProxy":                {http.MethodPost},
		"/api/torrentsInfos":            {http.MethodPost},
		"/api/trackersUpdate":           {http.MethodPost},
		"/api/update":                   {http.MethodPost},
		"/api/updateTotalEpisodeNumber": {http.MethodPost},
		"/api/upload":                   {http.MethodPost},
		"/api/uploadAndRead":            {http.MethodPost},
		"/api/uploadAndReadToBase64":    {http.MethodPost},
		"/api/webui/delete":             {http.MethodPost},
		"/api/webui/getUpdate":          {http.MethodPost},
		"/api/webui/update":             {http.MethodPost},
		"/api/webui/upload":             {http.MethodPost},
	}
	expectedSet := make(map[string]map[string]bool, len(expected))
	for routePath, methods := range expected {
		expectedSet[routePath] = make(map[string]bool, len(methods))
		for _, method := range methods {
			expectedSet[routePath][method] = true
		}
	}

	actual := map[string]map[string]bool{}
	for _, route := range app.Routes() {
		if actual[route.Path] == nil {
			actual[route.Path] = map[string]bool{}
		}
		actual[route.Path][route.Method] = true
	}
	if len(actual) != len(expected) {
		t.Fatalf("core route count = %d, want %d; actual-only=%v expected-only=%v", len(actual), len(expected), routeDifference(actual, expectedSet), routeDifference(expectedSet, actual))
	}
	for routePath, methods := range expected {
		for _, method := range methods {
			if !actual[routePath][method] {
				t.Errorf("missing route %s %s", method, routePath)
			}
		}
	}
	for routePath := range actual {
		if _, ok := expected[routePath]; !ok {
			t.Errorf("unexpected core route %s", routePath)
		}
	}
}

func routeDifference(left, right map[string]map[string]bool) []string {
	missing := make([]string, 0)
	for routePath, methods := range left {
		for method := range methods {
			if !right[routePath][method] {
				missing = append(missing, method+" "+routePath)
			}
		}
	}
	return missing
}
