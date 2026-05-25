package podimo

import (
	"sync"
	"testing"
	"time"
)

func TestBoundedMap_GetSet(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	bm.Set("a", 1, time.Hour)
	v, ok := bm.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected cache hit with value 1, got %v, ok=%v", v, ok)
	}
}

func TestBoundedMap_MissingKey(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	_, ok := bm.Get("missing")
	if ok {
		t.Fatalf("expected miss")
	}
}

func TestBoundedMap_TTLExpiration(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{
		MaxSize:         10,
		TTL:             50 * time.Millisecond,
		CleanupInterval: 50 * time.Millisecond,
	})
	bm.Set("a", 1, 10*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	_, ok := bm.Get("a")
	if ok {
		t.Fatalf("expected expiration after TTL")
	}
	bm.Stop()
}

func TestBoundedMap_MaxSizeEviction(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 2})
	bm.Set("a", 1, time.Hour)
	bm.Set("b", 2, time.Hour)
	bm.Set("c", 3, time.Hour)
	if _, ok := bm.Get("a"); ok {
		t.Fatalf("expected a to be evicted (LRU)")
	}
	if _, ok := bm.Get("b"); !ok {
		t.Fatalf("expected b to exist")
	}
	if _, ok := bm.Get("c"); !ok {
		t.Fatalf("expected c to exist")
	}
}

func TestBoundedMap_LRUUpdateOnGet(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 2})
	bm.Set("a", 1, time.Hour)
	bm.Set("b", 2, time.Hour)
	bm.Get("a")               // touch a, moves to front
	bm.Set("c", 3, time.Hour) // should evict b, not a
	if _, ok := bm.Get("a"); !ok {
		t.Fatalf("expected a to exist (recently touched)")
	}
	if _, ok := bm.Get("b"); ok {
		t.Fatalf("expected b to be evicted (LRU)")
	}
}

func TestBoundedMap_ConcurrentAccess(t *testing.T) {
	bm := NewBoundedMap[int, int](BoundedMapOptions{MaxSize: 100})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bm.Set(n, n, time.Hour)
			if v, ok := bm.Get(n); !ok || v != n {
				t.Errorf("expected %d, got %v, ok=%v", n, v, ok)
			}
		}(i)
	}
	wg.Wait()
}

func TestBoundedMap_GetOrSet(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	v := bm.GetOrSet("a", func() int { return 42 }, time.Hour)
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	v2 := bm.GetOrSet("a", func() int { return 99 }, time.Hour)
	if v2 != 42 {
		t.Fatalf("expected cached 42, got %d", v2)
	}
}

func TestBoundedMap_Delete(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	bm.Set("a", 1, time.Hour)
	bm.Delete("a")
	if _, ok := bm.Get("a"); ok {
		t.Fatalf("expected delete to remove key")
	}
}

func TestBoundedMap_Len(t *testing.T) {
	bm := NewBoundedMap[string, int](BoundedMapOptions{MaxSize: 10})
	if bm.Len() != 0 {
		t.Fatalf("expected len 0, got %d", bm.Len())
	}
	bm.Set("a", 1, time.Hour)
	if bm.Len() != 1 {
		t.Fatalf("expected len 1, got %d", bm.Len())
	}
}
