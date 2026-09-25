package subscription_test

import (
	"strings"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

func FuzzValidateItemsMatchesBoundaryRules(f *testing.F) {
	f.Add("one", "Demo", "https://example.test/rss", 1)
	f.Add("", "", "", -1)
	f.Add(" one ", " Demo ", " https://example.test/rss ", 0)
	f.Fuzz(func(t *testing.T, id, title, rawURL string, season int) {
		err := subscription.ValidateItems([]model.Ani{{ID: id, Title: title, URL: rawURL, Season: season}})
		valid := strings.TrimSpace(id) != "" && strings.TrimSpace(title) != "" && strings.TrimSpace(rawURL) != "" && season >= 0
		if valid && err != nil {
			t.Fatalf("valid subscription rejected: %v", err)
		}
		if !valid && err == nil {
			t.Fatalf("invalid subscription accepted: id=%q title=%q url=%q season=%d", id, title, rawURL, season)
		}
	})
}
