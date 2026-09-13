package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/shijie152/ani-rss/go-backend/internal/model"
)

const sqliteFilename = "ani-rss.sqlite"

// SQLiteStore is the final application store. Each logical document is kept
// as JSON inside a transactional SQLite row so the public model can continue
// evolving without a schema migration for every UI field.
type SQLiteStore struct {
	dir string
	db  *sql.DB
	mu  sync.RWMutex
}

// NewSQLiteStore opens the final store and imports legacy JSON exactly once
// when the database has no state yet. The legacy files are not read by the
// business modules after this constructor returns.
func NewSQLiteStore(dir string) (*SQLiteStore, error) {
	legacy, err := NewJSONStore(dir)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(filepath.Join(legacy.Directory(), sqliteFilename)) + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite store: %w", err)
	}
	store := &SQLiteStore{dir: legacy.Directory(), db: db}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_state`).Scan(&count); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("inspect SQLite state: %w", err)
	}
	if count == 0 {
		config, configErr := legacy.LoadConfig()
		items, itemsErr := legacy.LoadSubscriptions()
		resources, resourcesErr := legacy.LoadResources()
		tasks, tasksErr := legacy.LoadTasks()
		if configErr != nil || itemsErr != nil || resourcesErr != nil || tasksErr != nil {
			_ = db.Close()
			return nil, firstStoreError(configErr, itemsErr, resourcesErr, tasksErr)
		}
		if err := store.ReplaceStateWithTasks(config, items, resources, tasks); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("import legacy JSON into SQLite: %w", err)
		}
	}
	return store, nil
}

func (s *SQLiteStore) Directory() string { return s.dir }

// DatabasePath returns the on-disk database path for diagnostics and backup
// tooling. Callers must not open or mutate this file directly.
func (s *SQLiteStore) DatabasePath() string { return filepath.Join(s.dir, sqliteFilename) }

func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *SQLiteStore) initialize() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS app_state (
		name TEXT PRIMARY KEY,
		payload BLOB NOT NULL,
		updated_at INTEGER NOT NULL DEFAULT (unixepoch())
	);
	CREATE INDEX IF NOT EXISTS app_state_updated_at ON app_state(updated_at);`)
	if err != nil {
		return fmt.Errorf("initialize SQLite schema: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LoadConfig() (model.Config, error) {
	var value model.Config
	if err := s.load("config", &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("SQLite config is null")
	}
	return value, nil
}

func (s *SQLiteStore) SaveConfig(value model.Config) error { return s.save("config", value) }

func (s *SQLiteStore) LoadSubscriptions() ([]model.Ani, error) {
	var value []model.Ani
	if err := s.load("subscriptions", &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("SQLite subscriptions is null")
	}
	return value, nil
}

func (s *SQLiteStore) SaveSubscriptions(value []model.Ani) error {
	return s.save("subscriptions", value)
}

func (s *SQLiteStore) LoadResources() ([]model.Resource, error) {
	var value []model.Resource
	if err := s.load("resources", &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("SQLite resources is null")
	}
	return value, nil
}

func (s *SQLiteStore) SaveResources(value []model.Resource) error { return s.save("resources", value) }

func (s *SQLiteStore) LoadTasks() ([]model.Torrent, error) {
	var value []model.Torrent
	if err := s.load("tasks", &value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []model.Torrent{}, nil
		}
		return nil, err
	}
	if value == nil {
		return nil, errors.New("SQLite tasks is null")
	}
	return value, nil
}

func (s *SQLiteStore) SaveTasks(value []model.Torrent) error { return s.save("tasks", value) }

// ReplaceState is used by backup restore and legacy import. All logical
// documents become visible together, so a failed restore cannot leave the
// running process with a mixed configuration/subscription/history snapshot.
func (s *SQLiteStore) ReplaceState(config model.Config, items []model.Ani, resources []model.Resource) error {
	tasks, err := s.LoadTasks()
	if err != nil {
		return err
	}
	return s.ReplaceStateWithTasks(config, items, resources, tasks)
}

// ReplaceStateWithTasks atomically replaces all logical application state,
// including the last known downloader task snapshot.
func (s *SQLiteStore) ReplaceStateWithTasks(config model.Config, items []model.Ani, resources []model.Resource, tasks []model.Torrent) error {
	if config == nil || items == nil || resources == nil || tasks == nil {
		return errors.New("SQLite state cannot contain null")
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return err
	}
	resourcesJSON, err := json.Marshal(resources)
	if err != nil {
		return err
	}
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite state transaction: %w", err)
	}
	for name, payload := range map[string][]byte{"config": configJSON, "subscriptions": itemsJSON, "resources": resourcesJSON, "tasks": tasksJSON} {
		if _, err := tx.Exec(`INSERT INTO app_state(name, payload, updated_at) VALUES (?, ?, unixepoch()) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload, updated_at=excluded.updated_at`, name, payload); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("write SQLite %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite state transaction: %w", err)
	}
	return nil
}

func (s *SQLiteStore) load(name string, target any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var payload []byte
	if err := s.db.QueryRow(`SELECT payload FROM app_state WHERE name = ?`, name).Scan(&payload); err != nil {
		return fmt.Errorf("load SQLite %s: %w", name, err)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode SQLite %s: %w", name, err)
	}
	return nil
}

func (s *SQLiteStore) save(name string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode SQLite %s: %w", name, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT INTO app_state(name, payload, updated_at) VALUES (?, ?, unixepoch()) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload, updated_at=excluded.updated_at`, name, payload); err != nil {
		return fmt.Errorf("save SQLite %s: %w", name, err)
	}
	return nil
}

func firstStoreError(storeErrors ...error) error {
	for _, err := range storeErrors {
		if err != nil {
			return err
		}
	}
	return errors.New("unknown store error")
}
