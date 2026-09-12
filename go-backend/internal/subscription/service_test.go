package subscription_test

import (
	"testing"

	appconfig "github.com/shijie152/ani-rss/go-backend/internal/config"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/store"
	"github.com/shijie152/ani-rss/go-backend/internal/subscription"
)

func TestServiceValidatesDuplicatesAndPersistsEdits(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, m, nil)
	item := model.Ani{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 1, Enable: true}
	if err := service.Add(item); err != nil {
		t.Fatal(err)
	}
	if err := service.Add(item); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	item.Title = "Demo 2"
	item.Enable = false
	if err := service.Set(item); err != nil {
		t.Fatal(err)
	}
	items, err := s.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Demo 2" || items[0].Enable {
		t.Fatalf("persisted items = %#v", items)
	}
	if err := service.BatchEnable(true, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if !service.Items()[0].Enable {
		t.Fatal("batch enable did not update")
	}
	if err := service.Delete([]string{"one"}, false); err != nil {
		t.Fatal(err)
	}
	if len(service.Items()) != 0 {
		t.Fatal("delete did not remove item")
	}
}

func TestServiceListReturnsUIGroupingAndDownloadPath(t *testing.T) {
	s, err := store.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := appconfig.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	service := subscription.NewService(s, m, []model.Ani{{ID: "one", Title: "Demo", URL: "https://example.test/rss", Season: 2, ReleaseDate: "2025-03-04"}})
	list := service.List()
	if list.Total != 1 || len(list.WeekList) != 7 || len(list.ReleaseDateList) != 1 {
		t.Fatalf("list = %#v", list)
	}
	path, err := service.DownloadPath(service.Items()[0])
	if err != nil {
		t.Fatal(err)
	}
	if path["downloadPath"] == "" {
		t.Fatal("download path empty")
	}
}
