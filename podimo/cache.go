package podimo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileCache struct {
	dir      string
	keyLocks *BoundedMap[string, *sync.Mutex]
	dirMode  os.FileMode
	fileMode os.FileMode
}

type cacheEntry struct {
	ExpiresAt time.Time   `json:"expires_at"`
	Value     interface{} `json:"value"`
}

func NewFileCache(dir string) (*FileCache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	c := &FileCache{
		dir:      dir,
		dirMode:  0755,
		fileMode: 0644,
		keyLocks: NewBoundedMap[string, *sync.Mutex](BoundedMapOptions{
			MaxSize: 0, // unbounded to avoid evicting live mutexes
		}),
	}
	_ = os.Chmod(dir, c.dirMode)
	return c, nil
}

// NewSecureFileCache creates a FileCache with restrictive permissions (0700 dir,
// 0600 files) suitable for storing sensitive data such as Podimo auth tokens.
// Pre-existing dirs/files with looser perms are migrated via chmod.
func NewSecureFileCache(dir string) (*FileCache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	c := &FileCache{
		dir:      dir,
		dirMode:  0700,
		fileMode: 0600,
		keyLocks: NewBoundedMap[string, *sync.Mutex](BoundedMapOptions{
			MaxSize: 0, // unbounded to avoid evicting live mutexes
		}),
	}
	_ = os.Chmod(dir, c.dirMode)
	return c, nil
}

func (c *FileCache) getKeyLock(key string) *sync.Mutex {
	return c.keyLocks.GetOrSet(key, func() *sync.Mutex {
		return &sync.Mutex{}
	}, 0)
}

func (c *FileCache) Get(key string) (interface{}, bool) {
	l := c.getKeyLock(key)
	l.Lock()
	defer l.Unlock()

	path := filepath.Join(c.dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}
	if time.Now().After(entry.ExpiresAt) {
		_ = os.Remove(path)
		return nil, false
	}
	return entry.Value, true
}

func (c *FileCache) Set(key string, value interface{}, ttl time.Duration) error {
	l := c.getKeyLock(key)
	l.Lock()
	defer l.Unlock()

	path := filepath.Join(c.dir, key+".json")
	entry := cacheEntry{
		ExpiresAt: time.Now().Add(ttl),
		Value:     value,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, c.fileMode); err != nil {
		return err
	}
	_ = os.Chmod(path, c.fileMode)
	return nil
}
