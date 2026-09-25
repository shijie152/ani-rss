package rss_test

import (
	"fmt"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
	"github.com/shijie152/ani-rss/go-backend/internal/rss"
)

func FuzzMatchMaintainsResourceInvariants(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4}, int8(0), false)
	f.Add([]byte{8, 8, 2, 1}, int8(-1), true)
	f.Add([]byte("episode 01 [1080p]"), int8(2), false)
	f.Fuzz(func(t *testing.T, input []byte, offset int8, downloadNew bool) {
		items := make([]model.Resource, 0, len(input))
		for index, value := range input {
			group := "Group"
			if index%3 == 0 {
				group = ""
			}
			items = append(items, model.Resource{
				Title:    fmt.Sprintf("Episode %02d %c", int(value%12)+1, value),
				Episode:  float64(value%12) + 1,
				Subgroup: group,
			})
		}

		result := rss.Match(items, model.Ani{Subgroup: "Group"}, rss.MatchOptions{
			Offset:      int(offset % 3),
			DownloadNew: downloadNew,
		})

		seenEpisodes := map[float64]bool{}
		for index, item := range result {
			if item.Episode <= 0 {
				t.Fatalf("result[%d] has non-positive episode %v", index, item.Episode)
			}
			if item.Subgroup != "Group" {
				t.Fatalf("result[%d] subgroup = %q, want Group", index, item.Subgroup)
			}
			if !item.Master {
				t.Fatalf("result[%d] was not marked as the master resource", index)
			}
			if index > 0 && result[index-1].Episode > item.Episode {
				t.Fatalf("episodes are not sorted: %v before %v", result[index-1].Episode, item.Episode)
			}
			if seenEpisodes[item.Episode] {
				t.Fatalf("duplicate episode %v returned while coexist is disabled", item.Episode)
			}
			seenEpisodes[item.Episode] = true
		}
	})
}

func FuzzPlanSubmissionIsIdempotent(f *testing.F) {
	f.Add([]byte{1, 2, 2, 3})
	f.Add([]byte("same-title"))
	f.Fuzz(func(t *testing.T, input []byte) {
		resources := make([]model.Resource, 0, len(input))
		history := make([]model.Resource, 0)
		tasks := make([]model.Torrent, 0)
		for index, value := range input {
			resource := model.Resource{
				Title:       fmt.Sprintf("Episode %d", value%4),
				DownloadURL: fmt.Sprintf("https://example.test/%d", value%4),
				InfoHash:    fmt.Sprintf("hash-%d", value%4),
			}
			resources = append(resources, resource)
			if index%5 == 0 {
				history = append(history, resource)
			} else if index%5 == 1 {
				tasks = append(tasks, model.Torrent{Hash: resource.InfoHash})
			} else if index%5 == 2 {
				tasks = append(tasks, model.Torrent{Name: resource.Title})
			}
		}

		planned := rss.PlanSubmission(resources, history, tasks)
		seen := map[string]bool{}
		for _, resource := range planned {
			key := resource.InfoHash
			if key == "" {
				key = resource.DownloadURL + "\x00" + resource.Title
			}
			if seen[key] {
				t.Fatalf("planned duplicate resource %q", key)
			}
			seen[key] = true
			for _, previous := range history {
				if resource.InfoHash != "" && resource.InfoHash == previous.InfoHash {
					t.Fatalf("planned resource already exists in history: %q", key)
				}
			}
			for _, task := range tasks {
				if resource.InfoHash != "" && resource.InfoHash == task.Hash || resource.Title == task.Name {
					t.Fatalf("planned resource already exists in downloader tasks: %q", key)
				}
			}
		}
	})
}
