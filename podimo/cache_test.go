package podimo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileCache_MissingKey(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileCache(dir)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	v, ok := c.Get("missing")
	if ok || v != nil {
		t.Fatalf("expected nil,false for missing key")
	}
}

func TestFileCache_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileCache(dir)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	if err := c.Set("key", "value", time.Hour); err != nil {
		t.Fatalf("set: %v", err)
	}
	path := filepath.Join(dir, "key.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Contains(data, []byte("expires_at")) {
		t.Fatalf("expected expires_at in JSON")
	}
	v, ok := c.Get("key")
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if v != "value" {
		t.Fatalf("expected value, got %v", v)
	}
}

func TestFileCache_Expiry(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileCache(dir)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	if err := c.Set("key", "value", time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	v, ok := c.Get("key")
	if ok || v != nil {
		t.Fatalf("expected nil,false after expiry")
	}
	_, err = os.Stat(filepath.Join(dir, "key.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted after expiry")
	}
}
