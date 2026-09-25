package torrent_test

import (
	"strings"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/torrent"
)

func FuzzParseMetainfoNeverReturnsUnsafeSuccess(f *testing.F) {
	f.Add([]byte("d4:infod4:name8:demo.mkv6:lengthi1eee"))
	f.Add([]byte("d4:infod4:name4:demo5:filesld6:lengthi1e4:pathl8:demo.mkv eee"))
	f.Add([]byte("not-a-torrent"))
	f.Fuzz(func(t *testing.T, data []byte) {
		result, err := torrent.Parse(data)
		if err != nil {
			return
		}
		if result.Name == "" || result.InfoHash == "" || len(result.Files) == 0 {
			t.Fatalf("successful parse returned incomplete metainfo: %#v", result)
		}
		for _, file := range result.Files {
			if file.Path == "" || file.Length < 0 || strings.HasPrefix(file.Path, "/") || strings.Contains(file.Path, "../") {
				t.Fatalf("successful parse returned unsafe file: %#v", file)
			}
		}
	})
}
