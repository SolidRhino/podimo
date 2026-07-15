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

func TestFileCache_SecureMode(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the dir and a file with loose perms to verify migration.
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	staleFile := filepath.Join(dir, "existing.json")
	if err := os.WriteFile(staleFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	c, err := NewSecureFileCache(dir)
	if err != nil {
		t.Fatalf("new secure cache: %v", err)
	}

	if err := c.Set("key", "value", time.Hour); err != nil {
		t.Fatalf("set: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("expected dir mode 0700, got %o", info.Mode().Perm())
	}

	keyPath := filepath.Join(dir, "key.json")
	info, err = os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected file mode 0600, got %o", info.Mode().Perm())
	}

	// The pre-existing loose file is migrated to 0600 on the next Set write.
	if err := c.Set("existing", "v", time.Hour); err != nil {
		t.Fatalf("set existing: %v", err)
	}
	info, err = os.Stat(staleFile)
	if err != nil {
		t.Fatalf("stat stale file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected migrated file mode 0600, got %o", info.Mode().Perm())
	}
}
