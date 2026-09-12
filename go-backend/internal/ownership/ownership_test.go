package ownership_test

import (
	"errors"
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
