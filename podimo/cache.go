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
	keyLocks map[string]*sync.Mutex
	locksMu  sync.Mutex
}

type cacheEntry struct {
	ExpiresAt time.Time   `json:"expires_at"`
	Value     interface{} `json:"value"`
}

func NewFileCache(dir string) (*FileCache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &FileCache{dir: dir, keyLocks: make(map[string]*sync.Mutex)}, nil
}

func (c *FileCache) getKeyLock(key string) *sync.Mutex {
	c.locksMu.Lock()
	defer c.locksMu.Unlock()
	if l, ok := c.keyLocks[key]; ok {
		return l
	}
	l := &sync.Mutex{}
	c.keyLocks[key] = l
	return l
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
	return os.WriteFile(path, data, 0644)
}
