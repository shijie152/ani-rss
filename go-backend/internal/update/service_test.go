package update_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/update"
)

func TestCheckSelectsPlatformAssetAndComputesUpdatePolicy(t *testing.T) {
	archive := []byte("release archive")
	digest := sha256.Sum256(archive)
	assetName := "ani-rss-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
	if runtime.GOOS == "darwin" {
		assetName = "ani-rss-macos-" + runtime.GOARCH + ".tar.gz"
	}
	if runtime.GOOS == "windows" {
		assetName = "ani-rss-windows-" + runtime.GOARCH + ".zip"
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm" {
		assetName = "ani-rss-linux-armv7.tar.gz"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "ani-rss-go" {
			t.Fatalf("user-agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":"v3.2.32","body":"notes","assets":[{"name":%q,"size":%d,"digest":"sha256:%s","browser_download_url":"https://download.example/release"}]}`, assetName, len(archive), hex.EncodeToString(digest[:]))
	}))
	defer server.Close()

	client := &update.Client{Endpoint: server.URL, Version: "3.2.31"}
	info, err := client.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "3.2.32" || !info.Update || !info.AutoUpdate || info.Size != int64(len(archive)) || info.SHA256 == "" {
		t.Fatalf("update info = %#v", info)
	}
}

func TestCheckDoesNotOfferMissingOrOlderRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v3.1.99","assets":[]}`)
	}))
	defer server.Close()

	info, err := (&update.Client{Endpoint: server.URL, Version: "3.2.31"}).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.Update || info.URL != "" {
		t.Fatalf("unexpected update info = %#v", info)
	}
}
