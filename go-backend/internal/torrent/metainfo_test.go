package torrent_test

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strconv"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/torrent"
)

func TestParseSingleAndMultiFileMetainfo(t *testing.T) {
	info := bdict(map[string][]byte{
		"files": blist(
			bdict(map[string][]byte{"length": bint(100), "path": blist(bstr("[Group] Demo E01.mkv"))}),
			bdict(map[string][]byte{"length": bint(20), "path": blist(bstr("[Group] Demo E01.chs.ass"))}),
		),
		"name": bstr("Demo Collection"),
	})
	metainfo, err := torrent.Parse(bdict(map[string][]byte{"announce": bstr("https://tracker.invalid"), "info": info}))
	if err != nil {
		t.Fatal(err)
	}
	if metainfo.Name != "Demo Collection" || len(metainfo.Files) != 2 {
		t.Fatalf("metainfo = %#v", metainfo)
	}
	if metainfo.Files[0].Path != "Demo Collection/[Group] Demo E01.mkv" || metainfo.Files[1].Length != 20 {
		t.Fatalf("files = %#v", metainfo.Files)
	}
	hash := sha1.Sum(info)
	if metainfo.InfoHash != hex.EncodeToString(hash[:]) {
		t.Fatalf("info hash = %s", metainfo.InfoHash)
	}

	singleInfo := bdict(map[string][]byte{"length": bint(7), "name": bstr("one.mkv")})
	single, err := torrent.Parse(bdict(map[string][]byte{"info": singleInfo}))
	if err != nil || len(single.Files) != 1 || single.Files[0].Path != "one.mkv" {
		t.Fatalf("single = %#v, err=%v", single, err)
	}
}

func TestParseRejectsUnsafeTorrentPaths(t *testing.T) {
	bad := bdict(map[string][]byte{
		"files": blist(bdict(map[string][]byte{"length": bint(1), "path": blist(bstr(".."), bstr("outside.mkv"))})),
		"name":  bstr("Demo"),
	})
	if _, err := torrent.Parse(bdict(map[string][]byte{"info": bad})); err == nil {
		t.Fatal("unsafe torrent path was accepted")
	}
}

func bstr(value string) []byte { return []byte(strconv.Itoa(len(value)) + ":" + value) }
func bint(value int64) []byte  { return []byte("i" + strconv.FormatInt(value, 10) + "e") }
func blist(values ...[]byte) []byte {
	result := []byte("l")
	for _, value := range values {
		result = append(result, value...)
	}
	return append(result, 'e')
}
func bdict(values map[string][]byte) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []byte("d")
	for _, key := range keys {
		result = append(result, bstr(key)...)
		result = append(result, values[key]...)
	}
	return append(result, 'e')
}
