package downloader_test

import (
	"context"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/downloader"
	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

// minimalAdapter implements only the stable Adapter surface: no file listing,
// no rename, no per-file priority.
type minimalAdapter struct{}

func (minimalAdapter) Login(context.Context) error { return nil }
func (minimalAdapter) Add(context.Context, model.Resource, string, []string, bool) error {
	return nil
}
func (minimalAdapter) Torrents(context.Context) ([]model.Torrent, error) { return nil, nil }
func (minimalAdapter) WaitForCompletion(context.Context, string, time.Duration) (model.Torrent, error) {
	return model.Torrent{}, nil
}
func (minimalAdapter) Delete(context.Context, string, bool) error               { return nil }
func (minimalAdapter) RenameFile(context.Context, string, string, string) error { return nil }
func (minimalAdapter) AddTags(context.Context, string, string) error            { return nil }
func (minimalAdapter) SetSavePath(context.Context, string, string) error        { return nil }
func (minimalAdapter) Start(context.Context, string) error                      { return nil }
func (minimalAdapter) UpdateTrackers(context.Context, []string) error           { return nil }

func TestCapabilitiesAbsentForAdapterWithoutFileAccess(t *testing.T) {
	capabilities := downloader.Capabilities(minimalAdapter{})

	if capabilities.ListFiles != nil {
		t.Fatal("adapter without file listing must not expose ListFiles")
	}
	if capabilities.SetPriority != nil {
		t.Fatal("adapter without priority support must not expose SetPriority")
	}
	// RenameFile is part of the stable Adapter surface, so rename is always
	// available and never probed for.
	if capabilities.RenameFileInTask == nil {
		t.Fatal("rename must be wired for every adapter")
	}
}

// fileAwareAdapter adds the optional qBittorrent-style surface on top of the
// minimal adapter.
type fileAwareAdapter struct{ minimalAdapter }

func (fileAwareAdapter) Files(context.Context, string) ([]downloader.TorrentFile, error) {
	return []downloader.TorrentFile{{Index: 2, Name: "第01话.mkv", Size: 1024, Priority: 1}}, nil
}

func (fileAwareAdapter) SetFilePriority(context.Context, string, int, int) error { return nil }

func TestCapabilitiesAdaptFileShapeForFileAwareAdapter(t *testing.T) {
	capabilities := downloader.Capabilities(fileAwareAdapter{})

	if capabilities.ListFiles == nil || capabilities.SetPriority == nil {
		t.Fatal("file-aware adapter must expose both optional operations")
	}
	files, err := capabilities.ListFiles(context.Background(), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Index != 2 || files[0].Name != "第01话.mkv" || files[0].Size != 1024 || files[0].Priority != 1 {
		t.Fatalf("files = %#v", files)
	}
}
