//go:build gui && (darwin || windows)

// Package desktop contains the optional native desktop shell. The HTTP server
// remains the same binary and the tray is enabled only by the --gui flag.
package desktop

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/getlantern/systray"
)

// Start runs the native notification-area/menu-bar shell. It is intended to
// be called in a goroutine after the HTTP server has been configured.
func Start(listenAddress, configDirectory, uiDirectory string) {
	webURL := "http://127.0.0.1:7789/"
	if port := portFromListenAddress(listenAddress); port != "" {
		webURL = "http://127.0.0.1:" + port + "/"
	}
	systray.Run(func() {
		systray.SetTitle("ANI-RSS")
		if icon, err := os.ReadFile(filepath.Join(uiDirectory, "icon-128.png")); err == nil {
			systray.SetIcon(icon)
		}
		web := systray.AddMenuItem("WebUI", "打开 ANI-RSS WebUI")
		config := systray.AddMenuItem("Config", "打开 ANI-RSS 配置目录")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "退出 ANI-RSS")
		go func() {
			for {
				select {
				case <-web.ClickedCh:
					open(webURL)
				case <-config.ClickedCh:
					open(configDirectory)
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}

// Stop closes the native tray loop when the HTTP stop endpoint requests a
// shutdown. It is a no-op for server-only builds through tray_stub.go.
func Stop() { systray.Quit() }

func portFromListenAddress(value string) string {
	value = strings.TrimSpace(value)
	if _, port, err := netSplitHostPort(value); err == nil {
		return port
	}
	if index := strings.LastIndexByte(value, ':'); index >= 0 {
		return strings.Trim(value[index+1:], "[]")
	}
	return ""
}

// Kept as a small wrapper so the GUI file has no platform-specific parsing.
func netSplitHostPort(value string) (string, string, error) {
	if strings.HasPrefix(value, ":") {
		return "", strings.TrimPrefix(value, ":"), nil
	}
	last := strings.LastIndexByte(value, ':')
	if last < 0 || last == len(value)-1 {
		return "", "", os.ErrInvalid
	}
	return value[:last], value[last+1:], nil
}

func open(value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if _, err := url.Parse(value); err != nil {
		return
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", value).Start()
		return
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", value).Start()
}
