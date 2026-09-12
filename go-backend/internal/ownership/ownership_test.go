package ownership_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shijie152/ani-rss/go-backend/internal/ownership"
)

func TestOwnershipPreventsSecondRuntimeAndReleases(t *testing.T) {
	dir := t.TempDir()
	first, err := ownership.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ownership.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Acquire("rss", "go"); err != nil {
		t.Fatal(err)
	}
	if err := second.Acquire("rss", "java"); !errors.Is(err, ownership.ErrAlreadyOwned) {
		t.Fatalf("second acquire = %v", err)
	}
	if err := first.Release("rss"); err != nil {
		t.Fatal(err)
	}
	if err := second.Acquire("rss", "java"); err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestOwnershipClaimsMigrationSchedulerDomainsAsOneRuntime(t *testing.T) {
	dir := t.TempDir()
	first, err := ownership.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ownership.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"rss", "rename", "maintenance"} {
		if err := first.Acquire(domain, "go"); err != nil {
			t.Fatalf("acquire %s: %v", domain, err)
		}
	}
	if owner, ok := first.Owner("rename"); !ok || owner != "go" {
		t.Fatalf("rename owner = %q, %v", owner, ok)
	}
	if err := second.Acquire("maintenance", "java"); !errors.Is(err, ownership.ErrAlreadyOwned) {
		t.Fatalf("second maintenance acquire = %v", err)
	}
	first.Close()
	if err := second.Acquire("maintenance", "java"); err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestOwnershipRecoversLockFromExitedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process probing differs on Windows")
	}
	process := exec.Command("sh", "-c", "exit 0")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	pid := process.Process.Pid
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "runtime-rss.lock")
	data, err := json.Marshal(map[string]any{"owner": "crashed-go", "pid": pid})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := ownership.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Acquire("rss", "go"); err != nil {
		t.Fatalf("stale lock was not recovered: %v", err)
	}
	manager.Close()
}

func TestOwnershipCloseDoesNotRemoveAReplacedOwnerLock(t *testing.T) {
	dir := t.TempDir()
	manager, err := ownership.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Acquire("rss", "go"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "runtime-rss.lock")
	data, err := json.Marshal(map[string]any{"owner": "new-go", "pid": os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("replaced owner lock was removed: %v", err)
	}
}
