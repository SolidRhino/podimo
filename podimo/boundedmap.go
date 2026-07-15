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
	OnEvict         func(any)
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
	v, evicted, ok := bm.get(key)
	bm.mu.Unlock()
	bm.fireCallbacks(evicted)
	return v, ok
}

// get returns the value and ok. If the entry expired it is removed and returned
// in evicted so the caller can fire OnEvict after releasing the lock.
func (bm *BoundedMap[K, V]) get(key K) (V, []V, bool) {
	e, ok := bm.items[key]
	if !ok {
		var zero V
		return zero, nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		v, _ := bm.removeEntry(key)
		var zero V
		return zero, []V{v}, false
	}
	bm.lru.MoveToFront(e.elem)
	return e.value, nil, true
}

// Peek returns the value for key without updating LRU recency and without taking
// a write lock. Expired entries are reported as misses but not removed (the
// background cleanup or a subsequent Set/Get handles removal).
func (bm *BoundedMap[K, V]) Peek(key K) (V, bool) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	e, ok := bm.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		var zero V
		return zero, false
	}
	return e.value, true
}

func (bm *BoundedMap[K, V]) Set(key K, value V, ttl time.Duration) {
	bm.mu.Lock()
	evicted := bm.set(key, value, ttl)
	bm.mu.Unlock()
	bm.fireCallbacks(evicted)
}

func (bm *BoundedMap[K, V]) set(key K, value V, ttl time.Duration) []V {
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
		return nil
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
		if v, ok := bm.evictLRU(); ok {
			return []V{v}
		}
	}
	return nil
}

func (bm *BoundedMap[K, V]) GetOrSet(key K, factory func() V, ttl time.Duration) V {
	bm.mu.Lock()
	v, evicted, ok := bm.get(key)
	if !ok {
		v = factory()
		more := bm.set(key, v, ttl)
		evicted = append(evicted, more...)
	}
	bm.mu.Unlock()
	bm.fireCallbacks(evicted)
	return v
}

func (bm *BoundedMap[K, V]) Delete(key K) {
	bm.mu.Lock()
	v, ok := bm.removeEntry(key)
	bm.mu.Unlock()
	if ok {
		bm.fireCallbacks([]V{v})
	}
}

// removeEntry removes the entry and returns its value. Caller holds the write
// lock. Returns the zero value and false if the key was absent.
func (bm *BoundedMap[K, V]) removeEntry(key K) (V, bool) {
	if e, ok := bm.items[key]; ok {
		bm.lru.Remove(e.elem)
		delete(bm.items, key)
		return e.value, true
	}
	var zero V
	return zero, false
}

// evictLRU removes the least-recently-used entry. Caller holds the write lock.
func (bm *BoundedMap[K, V]) evictLRU() (V, bool) {
	back := bm.lru.Back()
	if back == nil {
		var zero V
		return zero, false
	}
	key := back.Value.(K)
	return bm.removeEntry(key)
}

func (bm *BoundedMap[K, V]) fireCallbacks(values []V) {
	if bm.opts.OnEvict == nil || len(values) == 0 {
		return
	}
	for _, v := range values {
		bm.opts.OnEvict(v)
	}
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
	now := time.Now()
	var evicted []V
	for key, e := range bm.items {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			if v, ok := bm.removeEntry(key); ok {
				evicted = append(evicted, v)
			}
		}
	}
	bm.mu.Unlock()
	bm.fireCallbacks(evicted)
}
