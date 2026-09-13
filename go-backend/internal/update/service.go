// Package update implements the release check and self-update boundary for
// the Go binary. It understands the archive names emitted by the release
// scripts and never replaces a binary before its checksum and size validate.
package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Endpoint string
	Version  string
	Token    string
	HTTP     *http.Client
}

type Info struct {
	Version    string
	Update     bool
	AutoUpdate bool
	URL        string
	SHA256     string
	Size       int64
	FormatSize string
	Body       string
	Date       string
}

type release struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []asset   `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *Client) Check(ctx context.Context) (Info, error) {
	info := Info{Version: c.Version, FormatSize: "0 MiB"}
	if strings.TrimSpace(c.Endpoint) == "" {
		return info, nil
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return info, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ani-rss-go")
	if strings.TrimSpace(c.Token) != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return info, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return info, fmt.Errorf("release check returned HTTP %d", response.StatusCode)
	}
	var latest release
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&latest); err != nil {
		return info, fmt.Errorf("decode release metadata: %w", err)
	}
	info.Version = strings.TrimPrefix(strings.TrimPrefix(latest.TagName, "v"), "V")
	info.Body = latest.Body
	if !latest.PublishedAt.IsZero() {
		info.Date = latest.PublishedAt.Format("2006-01-02 15:04:05")
	}
	info.Update = compareVersion(info.Version, c.Version) > 0
	info.AutoUpdate = sameMinor(info.Version, c.Version)
	name := assetName()
	for _, candidate := range latest.Assets {
		if candidate.Name != name {
			continue
		}
		info.URL = candidate.BrowserDownloadURL
		info.Size = candidate.Size
		info.FormatSize = formatSize(candidate.Size)
		info.SHA256 = strings.TrimPrefix(candidate.Digest, "sha256:")
		break
	}
	if info.URL == "" {
		info.Update = false
	}
	return info, nil
}

// Install downloads, validates and stages the release. Unix binaries can be
// atomically replaced while running. Windows receives a detached cmd helper
// because the running executable is locked by the OS.
func (c *Client) Install(ctx context.Context, info Info) error {
	if !info.Update || strings.TrimSpace(info.URL) == "" {
		return errors.New("当前版本无需更新")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "ani-rss-go")
	if strings.TrimSpace(c.Token) != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download update returned HTTP %d", response.StatusCode)
	}
	archiveFile, err := os.CreateTemp("", "ani-rss-update-*")
	if err != nil {
		return err
	}
	archivePath := archiveFile.Name()
	defer os.Remove(archivePath)
	hash := sha256.New()
	var source io.Reader = response.Body
	if info.Size > 0 {
		source = io.LimitReader(response.Body, info.Size+1)
	}
	written, copyErr := io.Copy(io.MultiWriter(archiveFile, hash), source)
	if copyErr != nil {
		archiveFile.Close()
		return copyErr
	}
	if err := archiveFile.Close(); err != nil {
		return err
	}
	if info.Size > 0 && written != info.Size {
		return fmt.Errorf("update size mismatch: got %d, want %d", written, info.Size)
	}
	if info.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), info.SHA256) {
		return errors.New("更新文件 sha256 不匹配")
	}
	stage, err := os.MkdirTemp("", "ani-rss-update-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extract(archivePath, stage); err != nil {
		return err
	}
	binary, err := findBinary(stage)
	if err != nil {
		return err
	}
	current, err := os.Executable()
	if err != nil {
		return err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return stageWindowsUpdate(binary, current)
	}
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(current); statErr == nil {
		mode = info.Mode()
	}
	replacement, err := os.CreateTemp(filepath.Dir(current), ".ani-rss-replacement-")
	if err != nil {
		return err
	}
	replacementPath := replacement.Name()
	if err := copyFile(replacement, binary); err != nil {
		replacement.Close()
		os.Remove(replacementPath)
		return err
	}
	if err := replacement.Chmod(mode); err != nil {
		replacement.Close()
		os.Remove(replacementPath)
		return err
	}
	if err := replacement.Close(); err != nil {
		os.Remove(replacementPath)
		return err
	}
	if err := os.Rename(replacementPath, current); err != nil {
		os.Remove(replacementPath)
		return err
	}
	return nil
}

func assetName() string {
	arch := runtime.GOARCH
	if runtime.GOOS == "windows" {
		return "ani-rss-windows-" + arch + ".zip"
	}
	if runtime.GOOS == "darwin" {
		return "ani-rss-macos-" + arch + ".tar.gz"
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm" {
		return "ani-rss-linux-armv7.tar.gz"
	}
	return "ani-rss-" + runtime.GOOS + "-" + arch + ".tar.gz"
}

func extract(name, target string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	if strings.HasSuffix(name, ".zip") {
		return extractZip(name, target)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open update archive: %w", err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, readErr := tarReader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		path, ok := safePath(target, header.Name)
		if !ok {
			return errors.New("更新压缩包包含非法路径")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, io.LimitReader(tarReader, 256<<20))
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func extractZip(name, target string) error {
	archive, err := zip.OpenReader(name)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		path, ok := safePath(target, entry.Name)
		if !ok {
			return errors.New("更新压缩包包含非法路径")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, 256<<20))
		input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func safePath(root, name string) (string, bool) {
	name = filepath.FromSlash(strings.ReplaceAll(name, "\\", "/"))
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	path := filepath.Join(root, clean)
	return path, path == filepath.Clean(root) || strings.HasPrefix(path, filepath.Clean(root)+string(filepath.Separator))
}

func findBinary(root string) (string, error) {
	wanted := "ani-rss"
	if runtime.GOOS == "windows" {
		wanted += ".exe"
	}
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Base(path) == wanted {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("更新包中缺少 Go 可执行文件")
	}
	return found, nil
}

func copyFile(destination *os.File, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	_, err = io.Copy(destination, input)
	return err
}

func stageWindowsUpdate(source, target string) error {
	staged, err := os.CreateTemp("", "ani-rss-update-binary-*.exe")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	if err := copyFile(staged, source); err != nil {
		staged.Close()
		os.Remove(stagedPath)
		return err
	}
	if err := staged.Close(); err != nil {
		os.Remove(stagedPath)
		return err
	}
	arguments := make([]string, 0, len(os.Args)-1)
	for _, argument := range os.Args[1:] {
		arguments = append(arguments, "\""+strings.ReplaceAll(argument, "\"", "\\\"")+"\"")
	}
	start := "start \"\" \"" + target + "\""
	if len(arguments) > 0 {
		start += " " + strings.Join(arguments, " ")
	}
	commandLine := "timeout /t 2 /nobreak >nul & move /y \"" + stagedPath + "\" \"" + target + "\" & " + start
	command := exec.Command("cmd", "/d", "/c", commandLine)
	if err := command.Start(); err != nil {
		os.Remove(stagedPath)
		return err
	}
	return nil
}

func compareVersion(left, right string) int {
	parse := func(value string) []int {
		value = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "v"), "V")
		parts := strings.SplitN(value, "-", 2)[0]
		values := strings.Split(parts, ".")
		result := make([]int, 3)
		for index := 0; index < len(values) && index < len(result); index++ {
			result[index], _ = strconv.Atoi(values[index])
		}
		return result
	}
	a, b := parse(left), parse(right)
	for index := range a {
		if a[index] > b[index] {
			return 1
		}
		if a[index] < b[index] {
			return -1
		}
	}
	return 0
}

func sameMinor(left, right string) bool {
	if compareVersion(left, right) <= 0 {
		return false
	}
	parse := func(value string) string {
		parts := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "v"), "V"), ".", 3)
		if len(parts) < 2 {
			return ""
		}
		return parts[0] + "." + parts[1]
	}
	return parse(left) != "" && parse(left) == parse(right)
}

func formatSize(value int64) string {
	if value <= 0 {
		return "0 MiB"
	}
	return fmt.Sprintf("%.2f MiB", float64(value)/(1024*1024))
}
