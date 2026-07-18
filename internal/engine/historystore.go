package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HistoryStore persists notification history (notify / mute / suppress /
// recover events) as JSON-lines in a single file. Each event is one JSON
// object per line, appended on write. On startup the file is trimmed by
// retention window and the in-memory cap (maxHistory) so it does not grow
// without bound.
//
// Active alerts / incidents remain in memory only; this store is solely for
// history so a service restart keeps recent history (see plan B0).
type HistoryStore struct {
	mu        sync.Mutex
	path      string
	retention time.Duration
}

// NewHistoryStore opens (or creates) the history file and trims stale lines.
func NewHistoryStore(path string, retention time.Duration) (*HistoryStore, error) {
	if path == "" {
		path = "data/history.log"
	}
	if retention <= 0 {
		retention = 720 * time.Hour
	}
	s := &HistoryStore{path: path, retention: retention}
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	if err := s.trimLocked(); err != nil {
		// Non-fatal: log and continue with an empty history.
		log.Printf("WARNING: history store trim failed (%v), starting fresh", err)
	}
	return s, nil
}

// Path returns the backing file path.
func (s *HistoryStore) Path() string { return s.path }

func (s *HistoryStore) ensureDir() error {
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir history store: %w", err)
		}
	}
	return nil
}

// Append writes one history event as a JSON line (file append).
func (s *HistoryStore) Append(ev HistoryEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDir(); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open history store: %w", err)
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write history store: %w", err)
	}
	return nil
}

// Load reads all history events (newest last). Corrupted lines are skipped.
func (s *HistoryStore) Load() ([]HistoryEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *HistoryStore) loadLocked() ([]HistoryEvent, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history store: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	out := make([]HistoryEvent, 0, 64)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev HistoryEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			log.Printf("WARNING: skipping corrupted history line in %s: %v", s.path, err)
			continue
		}
		out = append(out, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan history store: %w", err)
	}
	return out, nil
}

// trimLocked drops events older than the retention window or beyond the
// in-memory cap, then rewrites the file atomically. Must hold s.mu.
func (s *HistoryStore) trimLocked() error {
	events, err := s.loadLocked()
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-s.retention)
	kept := events[:0]
	for _, ev := range events {
		if ev.Time.Before(cutoff) {
			continue
		}
		kept = append(kept, ev)
	}
	if len(kept) > maxHistory {
		kept = kept[len(kept)-maxHistory:]
	}
	if len(kept) == len(events) {
		return nil
	}
	return s.writeAllLocked(kept)
}

func (s *HistoryStore) writeAllLocked(events []HistoryEvent) error {
	data := make([]byte, 0, 1024)
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		data = append(data, b...)
		data = append(data, '\n')
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write history store tmp: %w", err)
	}
	return os.Rename(tmp, s.path)
}
