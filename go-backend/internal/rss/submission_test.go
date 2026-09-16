package rss_test

import (
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
)

func TestPlanSubmissionIsIdempotentAcrossHistoryDownloaderAndBatch(t *testing.T) {
	resources := []model.Resource{{Title: "one", DownloadURL: "magnet:?xt=urn:btih:ONE", InfoHash: "one"}, {Title: "two", DownloadURL: "magnet:?xt=urn:btih:TWO", InfoHash: "two"}, {Title: "three", DownloadURL: "magnet:?xt=urn:btih:THREE", InfoHash: "three"}}
	history := []model.Resource{resources[0]}
	tasks := []model.Torrent{{Hash: "TWO", Name: "two"}}
	planned := rss.PlanSubmission(append(resources, resources[2]), history, tasks)
	if len(planned) != 1 || planned[0].InfoHash != "three" {
		t.Fatalf("planned = %#v", planned)
	}
}
