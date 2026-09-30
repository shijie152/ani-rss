package backend

import (
	"context"
	"testing"
)

// 更新后的重启必须走装配方注入的钩子：关闭顺序（释放 ownership 锁）只应
// 存在于一处，处理器不能再自己拼 exec.Command。
func TestRestartAfterUpdateUsesTheInjectedHook(t *testing.T) {
	calls := 0
	app, err := New(Options{
		ConfigDir:        t.TempDir(),
		OwnershipDomains: []string{"runtime", "maintenance"},
		Restart: func(context.Context) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	if err := app.restartAfterUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("restart hook calls = %d, want 1", calls)
	}
}

func TestRestartAfterUpdateReportsMissingHook(t *testing.T) {
	app, err := New(Options{ConfigDir: t.TempDir(), OwnershipDomains: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	err = app.restartAfterUpdate(context.Background())
	if err == nil || err.Error() != "no restart hook is wired" {
		t.Fatalf("expected an error when no hook is wired, got %v", err)
	}
}
