package app

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func runWatcher(t *testing.T, dir string) (*watcher, func() ([]string, [][]string)) {
	t.Helper()
	watchInterval, watchSettle = 30*time.Millisecond, 150*time.Millisecond
	var mu sync.Mutex
	var ones []string
	var manys [][]string
	w := &watcher{dir: dir, stop: make(chan struct{})}
	w.onOne = func(p string) { mu.Lock(); ones = append(ones, p); mu.Unlock() }
	w.onMany = func(p []string) { mu.Lock(); manys = append(manys, p); mu.Unlock() }
	go w.run()
	t.Cleanup(func() { close(w.stop) })
	time.Sleep(80 * time.Millisecond) // baseline scan
	return w, func() ([]string, [][]string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), ones...), append([][]string(nil), manys...)
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("not really a dst but non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatchOpensSingleNewFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "old.dst")) // existing files are ignored
	_, got := runWatcher(t, dir)
	write(t, filepath.Join(dir, "new.DST"))
	write(t, filepath.Join(dir, "notes.txt"))
	time.Sleep(600 * time.Millisecond)
	ones, manys := got()
	if len(ones) != 1 || filepath.Base(ones[0]) != "new.DST" || len(manys) != 0 {
		t.Fatalf("ones=%v manys=%v", ones, manys)
	}
}

func TestWatchIgnoresBatch(t *testing.T) {
	dir := t.TempDir()
	_, got := runWatcher(t, dir)
	sub := filepath.Join(dir, "unzipped")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.dst", "b.dst", "c.dst"} {
		write(t, filepath.Join(sub, n))
	}
	time.Sleep(600 * time.Millisecond)
	ones, manys := got()
	if len(ones) != 0 || len(manys) != 1 || len(manys[0]) != 3 {
		t.Fatalf("ones=%v manys=%v", ones, manys)
	}
}

func TestWatchOpensReplacedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "order.dst")
	write(t, p)
	_, got := runWatcher(t, dir)
	if err := os.WriteFile(p, []byte("a different, longer design file body"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if ones, _ := got(); len(ones) != 1 {
		t.Fatalf("replacing a file with a new copy should open it, got %v", ones)
	}
}
