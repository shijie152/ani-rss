// Package ownership makes migration-time process ownership explicit. A
// domain has one lock file, so a second Go/Java runtime cannot silently start
// the same scheduler or state writer.
package ownership

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrAlreadyOwned = errors.New("runtime domain is already owned")

type Manager struct {
	dir   string
	mu    sync.Mutex
	owned map[string]string
}

type lockInfo struct {
	Owner      string    `json:"owner"`
	PID        int       `json:"pid"`
	AcquiredAt time.Time `json:"acquiredAt"`
}

func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{dir: dir, owned: map[string]string{}}, nil
}

func (m *Manager) Acquire(domain, owner string) error {
	if domain == "" || owner == "" {
		return errors.New("ownership domain and owner are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path := filepath.Join(m.dir, "runtime-"+safe(domain)+".lock")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrAlreadyOwned, domain)
		}
		return fmt.Errorf("acquire %s: %w", domain, err)
	}
	info := lockInfo{Owner: owner, PID: os.Getpid(), AcquiredAt: time.Now().UTC()}
	if err := json.NewEncoder(file).Encode(info); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	m.owned[domain] = owner
	return nil
}

func (m *Manager) Release(domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.owned[domain]; !ok {
		return nil
	}
	delete(m.owned, domain)
	return os.Remove(filepath.Join(m.dir, "runtime-"+safe(domain)+".lock"))
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for domain := range m.owned {
		_ = os.Remove(filepath.Join(m.dir, "runtime-"+safe(domain)+".lock"))
	}
	m.owned = map[string]string{}
}

func (m *Manager) Owner(domain string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owner, ok := m.owned[domain]
	return owner, ok
}

func safe(value string) string {
	result := make([]byte, 0, len(value))
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			result = append(result, byte(char))
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}
