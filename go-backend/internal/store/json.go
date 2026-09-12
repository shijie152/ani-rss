// Package store provides the persistence boundary used during migration.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

type Store interface {
	LoadConfig() (model.Config, error)
	SaveConfig(model.Config) error
	LoadSubscriptions() ([]model.Ani, error)
	SaveSubscriptions([]model.Ani) error
}

type HistoryStore interface {
	LoadResources() ([]model.Resource, error)
	SaveResources([]model.Resource) error
}

type JSONStore struct {
	dir string
	mu  sync.RWMutex
}

func NewJSONStore(dir string) (*JSONStore, error) {
	if dir == "" {
		dir = "config"
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve config directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	return &JSONStore{dir: dir}, nil
}

func (s *JSONStore) Directory() string { return s.dir }

func (s *JSONStore) LoadConfig() (model.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "config.v2.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return model.DefaultConfig(), nil
	}
	var config model.Config
	if err := readJSON(path, &config); err != nil {
		return nil, fmt.Errorf("read config.v2.json: %w", err)
	}
	if config == nil {
		return nil, errors.New("config.v2.json contains null instead of an object")
	}
	return config, nil
}

func (s *JSONStore) SaveConfig(config model.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSONAtomic(filepath.Join(s.dir, "config.v2.json"), config)
}

func (s *JSONStore) LoadSubscriptions() ([]model.Ani, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "ani.v2.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []model.Ani{}, nil
	}
	var items []model.Ani
	if err := readJSON(path, &items); err != nil {
		return nil, fmt.Errorf("read ani.v2.json: %w", err)
	}
	if items == nil {
		return nil, errors.New("ani.v2.json contains null instead of an array")
	}
	return items, nil
}

func (s *JSONStore) SaveSubscriptions(items []model.Ani) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSONAtomic(filepath.Join(s.dir, "ani.v2.json"), items)
}

func (s *JSONStore) LoadResources() ([]model.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "resources.v2.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []model.Resource{}, nil
	}
	var items []model.Resource
	if err := readJSON(path, &items); err != nil {
		return nil, fmt.Errorf("read resources.v2.json: %w", err)
	}
	if items == nil {
		return nil, errors.New("resources.v2.json contains null instead of an array")
	}
	return items, nil
}

func (s *JSONStore) SaveResources(items []model.Resource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSONAtomic(filepath.Join(s.dir, "resources.v2.json"), items)
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ani-rss-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary JSON file: %w", err)
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	encoder := json.NewEncoder(temp)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary JSON file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary JSON file: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace JSON file: %w", err)
	}
	ok = true
	return nil
}
