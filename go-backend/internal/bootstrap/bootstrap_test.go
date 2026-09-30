package bootstrap_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shijie152/ani-rss/go-backend/internal/bootstrap"
	"github.com/shijie152/ani-rss/go-backend/internal/testutil"
)

var client = testutil.LocalHTTPClient(10 * time.Second)

func TestRunServesRoutesForOwnedDomainsOnly(t *testing.T) {
	bound := make(chan string, 1)
	service, err := bootstrap.New(bootstrap.Options{
		ConfigDir:        t.TempDir(),
		ListenAddress:    "127.0.0.1:0",
		OwnershipDomains: []string{"runtime"},
		OnListening:      func(address string) { bound <- address },
	})
	if err != nil {
		t.Fatal(err)
	}
	// 生产里由信号取消 context；测试走同一条路径，而不是另开一个关闭入口。
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	var address string
	select {
	case address = <-bound:
	case <-time.After(10 * time.Second):
		t.Fatal("service never reported its bound address")
	}
	response, err := client.Get("http://" + address + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ping = %d", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after shutdown")
	}
}

func TestRestartRunsTheShutdownHookBeforeTheReplacementStarts(t *testing.T) {
	order := make(chan string, 2)
	bound := make(chan string, 1)
	configDir := t.TempDir()
	service, err := bootstrap.New(bootstrap.Options{
		ConfigDir:        configDir,
		ListenAddress:    "127.0.0.1:0",
		OwnershipDomains: []string{"runtime", "rss"},
		OnRestart:        func() { order <- "shutdown" },
		OnListening:      func(address string) { bound <- address },
		// 替代进程由测试命令替代：它在启动瞬间检查 domain 锁是否已释放。
		ResolveExecutable: func() (string, []string, error) {
			lock := filepath.Join(configDir, "locks", "runtime-rss.lock")
			if _, statErr := os.Stat(lock); statErr == nil {
				order <- "exec-with-lock-held"
			} else {
				order <- "exec-after-release"
			}
			return "true", nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 让服务真正抢到 domain 锁，模拟运行中的进程。
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	select {
	case <-bound:
	case <-time.After(10 * time.Second):
		t.Fatal("service never started")
	}
	// 关键：服务仍在运行（Run 还没走到收尾的 Close）时重启，模拟 update 处理器
	// 那条路径——此时 domain 锁只有 Restart 自己会释放。
	if err := service.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	close(order)
	got := make([]string, 0, 2)
	for value := range order {
		got = append(got, value)
	}
	if len(got) != 2 || got[0] != "shutdown" || got[1] != "exec-after-release" {
		t.Fatalf("order = %v", got)
	}
}
