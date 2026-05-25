package podimo

import (
	"container/list"
	"sync"
	"time"
)

type BoundedMapOptions struct {
	MaxSize         int
	TTL             time.Duration
	CleanupInterval time.Duration
}

type entry[V any] struct {
	value     V
	expiresAt time.Time
	elem      *list.Element
}

type BoundedMap[K comparable, V any] struct {
	opts     BoundedMapOptions
	mu       sync.RWMutex
	items    map[K]*entry[V]
	lru      *list.List
	stop     chan struct{}
	stopOnce sync.Once
}

func NewBoundedMap[K comparable, V any](opts BoundedMapOptions) *BoundedMap[K, V] {
	bm := &BoundedMap[K, V]{
		opts:  opts,
		items: make(map[K]*entry[V]),
		lru:   list.New(),
		stop:  make(chan struct{}),
	}
	if opts.CleanupInterval > 0 {
		go bm.cleanupLoop()
	}
	return bm
}

func (bm *BoundedMap[K, V]) Get(key K) (V, bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	return bm.get(key)
}

func (bm *BoundedMap[K, V]) get(key K) (V, bool) {
	e, ok := bm.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		bm.removeEntry(key)
		var zero V
		return zero, false
	}
	bm.lru.MoveToFront(e.elem)
	return e.value, true
}

func (bm *BoundedMap[K, V]) Set(key K, value V, ttl time.Duration) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.set(key, value, ttl)
}

func (bm *BoundedMap[K, V]) set(key K, value V, ttl time.Duration) {
	if e, ok := bm.items[key]; ok {
		e.value = value
		if ttl > 0 {
			e.expiresAt = time.Now().Add(ttl)
		} else if bm.opts.TTL > 0 {
			e.expiresAt = time.Now().Add(bm.opts.TTL)
		} else {
			e.expiresAt = time.Time{}
		}
		bm.lru.MoveToFront(e.elem)
		return
	}
	elem := bm.lru.PushFront(key)
	e := &entry[V]{value: value, elem: elem}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	} else if bm.opts.TTL > 0 {
		e.expiresAt = time.Now().Add(bm.opts.TTL)
	}
	bm.items[key] = e
	if bm.opts.MaxSize > 0 && bm.lru.Len() > bm.opts.MaxSize {
		bm.evictLRU()
	}
}

func (bm *BoundedMap[K, V]) GetOrSet(key K, factory func() V, ttl time.Duration) V {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if v, ok := bm.get(key); ok {
		return v
	}
	value := factory()
	bm.set(key, value, ttl)
	return value
}

func (bm *BoundedMap[K, V]) Delete(key K) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.removeEntry(key)
}

func (bm *BoundedMap[K, V]) removeEntry(key K) {
	if e, ok := bm.items[key]; ok {
		bm.lru.Remove(e.elem)
		delete(bm.items, key)
	}
}

func (bm *BoundedMap[K, V]) evictLRU() {
	back := bm.lru.Back()
	if back == nil {
		return
	}
	key := back.Value.(K)
	bm.removeEntry(key)
}

func (bm *BoundedMap[K, V]) Len() int {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return len(bm.items)
}

func (bm *BoundedMap[K, V]) Stop() {
	bm.stopOnce.Do(func() {
		close(bm.stop)
	})
}

func (bm *BoundedMap[K, V]) cleanupLoop() {
	ticker := time.NewTicker(bm.opts.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			bm.cleanup()
		case <-bm.stop:
			return
		}
	}
}

func (bm *BoundedMap[K, V]) cleanup() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	now := time.Now()
	for key, e := range bm.items {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			bm.removeEntry(key)
		}
	}
}
