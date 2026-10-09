package event

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// journalMaxRecords caps the in-memory seen-set. Journal rotation keeps the
// file from growing without bound; hitting the cap rotates early.
const journalMaxRecords = 100_000

// Journal persists accepted delivery ids so a redelivery of the same
// delivery_id (the host retries with a stable id while invocation_id changes
// per attempt) is recognized across restarts.
//
// Semantics are at-most-once by design: the id is recorded when the delivery
// is accepted, not after the notification lands, so a crash between the two
// loses that notification rather than duplicating it. For a status
// notification that is the right trade.
type Journal struct {
	mu   sync.Mutex
	path string
	seen map[string]struct{}
	file *os.File
}

// Open loads (or creates) the journal file. A missing parent directory is
// created; an unreadable existing file is an error worth surfacing.
func Open(path string) (*Journal, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create journal directory: %w", err)
		}
	}
	j := &Journal{path: path, seen: make(map[string]struct{})}
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", path, err)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if _, id, ok := strings.Cut(line, " "); ok {
			j.seen[id] = struct{}{}
		}
	}
	f.Close()
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open journal for append %s: %w", path, err)
	}
	j.file = f
	return j, nil
}

// Seen reports whether the delivery id was recorded before.
func (j *Journal) Seen(id string) bool {
	if j == nil || id == "" {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.seen[id]
	return ok
}

// Record appends the delivery id. Failures are returned; the caller decides
// whether to proceed (the hook server logs and continues — degraded
// dedupe beats a 500 the host would retry into more duplicates).
func (j *Journal) Record(id string) error {
	if j == nil || id == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.seen[id]; ok {
		return nil
	}
	if j.file == nil {
		j.seen[id] = struct{}{}
		return nil
	}
	if _, err := fmt.Fprintf(j.file, "%d %s\n", time.Now().Unix(), id); err != nil {
		return fmt.Errorf("append journal: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("sync journal: %w", err)
	}
	j.seen[id] = struct{}{}
	if len(j.seen) > journalMaxRecords {
		return j.rotateLocked()
	}
	return nil
}

// rotateLocked starts a fresh journal file, keeping the in-memory set so
// dedupe does not regress mid-run.
func (j *Journal) rotateLocked() error {
	j.file.Close()
	rotated := j.path + ".1"
	if err := os.Rename(j.path, rotated); err != nil {
		return fmt.Errorf("rotate journal: %w", err)
	}
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		j.file = nil
		return fmt.Errorf("reopen journal after rotate: %w", err)
	}
	j.file = f
	return nil
}

// Close releases the file handle. Pending writes were flushed per Record.
func (j *Journal) Close() error {
	if j == nil || j.file == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	err := j.file.Close()
	j.file = nil
	return err
}
