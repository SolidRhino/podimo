package podimo

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileCache struct {
	dir      string
	mem      *BoundedMap[string, cacheEntry]
	keyLocks [256]sync.Mutex
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
		mem:      NewBoundedMap[string, cacheEntry](BoundedMapOptions{MaxSize: 512}),
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
		mem:      NewBoundedMap[string, cacheEntry](BoundedMapOptions{MaxSize: 512}),
	}
	_ = os.Chmod(dir, c.dirMode)
	return c, nil
}

func (c *FileCache) getKeyLock(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &c.keyLocks[h.Sum32()%256]
}

func (c *FileCache) Get(key string) (interface{}, bool) {
	// Fast path: serve fresh entries from the in-memory cache without the
	// per-key lock. Expired entries are NOT touched here — they fall through to
	// the locked path so disk removal can never race a concurrent Set.
	if e, ok := c.mem.Peek(key); ok {
		if time.Now().Before(e.ExpiresAt) {
			return e.Value, true
		}
	}

	l := c.getKeyLock(key)
	l.Lock()
	defer l.Unlock()

	// Re-check memory under the per-key lock in case a concurrent Set raced us.
	if e, ok := c.mem.Peek(key); ok {
		if time.Now().After(e.ExpiresAt) {
			c.mem.Delete(key)
			_ = os.Remove(filepath.Join(c.dir, key+".json"))
			return nil, false
		}
		return e.Value, true
	}

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
	// Populate the in-memory layer for subsequent fast-path hits.
	c.mem.Set(key, entry, 0)
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
	c.mem.Set(key, entry, 0)
	return nil
}

// Delete removes a key from both the in-memory layer and disk. A missing file is
// treated as success so callers can invalidate already-absent tokens idempotently.
func (c *FileCache) Delete(key string) error {
	l := c.getKeyLock(key)
	l.Lock()
	defer l.Unlock()

	c.mem.Delete(key)
	if err := os.Remove(filepath.Join(c.dir, key+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
