package mute

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"smart-alert-aggregator/internal/alert"
)

// Rule suppresses notifications matching alertname / instance / hostname / labels.
// Time window: [StartsAt, ExpiresAt). Empty StartsAt = immediately; nil ExpiresAt = permanent.
type Rule struct {
	ID        string            `json:"id"`
	AlertName string            `json:"alertname"`
	Instance  string            `json:"instance,omitempty"`
	Hostname  string            `json:"hostname,omitempty"`
	Matchers  map[string]string `json:"matchers,omitempty"`
	Reason    string            `json:"reason"`
	CreatedAt time.Time         `json:"created_at"`
	StartsAt  *time.Time        `json:"starts_at,omitempty"`  // nil = from now
	ExpiresAt *time.Time        `json:"expires_at,omitempty"` // nil = permanent
}

func (r Rule) ActiveAt(now time.Time) bool {
	if r.StartsAt != nil && now.Before(*r.StartsAt) {
		return false
	}
	if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
		return false
	}
	return true
}

func (r Rule) StatusAt(now time.Time) string {
	if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
		return "expired"
	}
	if r.StartsAt != nil && now.Before(*r.StartsAt) {
		return "scheduled"
	}
	return "active"
}

// Store persists mute rules to a JSON file.
type Store struct {
	mu    sync.RWMutex
	path  string
	rules []Rule
}

func NewStore(path string) (*Store, error) {
	if path == "" {
		path = "data/mutes.json"
	}
	s := &Store{path: path, rules: make([]Rule, 0)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read mute store: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		log.Printf("WARNING: mute store %s is corrupted (%v), starting with empty rules", s.path, err)
		return nil
	}
	s.rules = rules
	s.purgeExpiredLocked(time.Now())
	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir mute store: %w", err)
		}
	}
	data, err := json.MarshalIndent(s.rules, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write mute store: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) List() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	out := make([]Rule, len(s.rules))
	copy(out, s.rules)
	return out
}

// CreateRequest is the API payload for adding a mute rule.
type CreateRequest struct {
	AlertName string            `json:"alertname"`
	Instance  string            `json:"instance"`
	Hostname  string            `json:"hostname"`
	Matchers  map[string]string `json:"matchers"`
	Reason    string            `json:"reason"`
	// Duration like "1h" / "4h" / "24h" / "168h". Empty + no expires_at = permanent.
	Duration string `json:"duration"`
	// Absolute window (RFC3339). Takes precedence over Duration when both set for ends.
	StartsAt  *time.Time `json:"starts_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Store) Add(req CreateRequest) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.purgeExpiredLocked(now)

	if req.AlertName == "" && req.Instance == "" && req.Hostname == "" && len(req.Matchers) == 0 {
		return Rule{}, fmt.Errorf("至少指定 alertname / instance / hostname / matchers 之一")
	}

	rule := Rule{
		ID:        fmt.Sprintf("mute-%d", now.UnixNano()),
		AlertName: req.AlertName,
		Instance:  req.Instance,
		Hostname:  req.Hostname,
		Matchers:  req.Matchers,
		Reason:    req.Reason,
		CreatedAt: now,
		StartsAt:  req.StartsAt,
		ExpiresAt: req.ExpiresAt,
	}

	if rule.ExpiresAt == nil && req.Duration != "" {
		d, err := time.ParseDuration(req.Duration)
		if err != nil {
			return Rule{}, fmt.Errorf("无效时长 duration=%q: %w", req.Duration, err)
		}
		if d <= 0 {
			return Rule{}, fmt.Errorf("时长必须为正数")
		}
		base := now
		if rule.StartsAt != nil {
			base = *rule.StartsAt
		}
		exp := base.Add(d)
		rule.ExpiresAt = &exp
	}

	if rule.StartsAt != nil && rule.ExpiresAt != nil && !rule.ExpiresAt.After(*rule.StartsAt) {
		return Rule{}, fmt.Errorf("结束时间必须晚于开始时间")
	}

	s.rules = append(s.rules, rule)
	if err := s.saveLocked(); err != nil {
		s.rules = s.rules[:len(s.rules)-1]
		return Rule{}, err
	}
	return rule, nil
}

func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if r.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			_ = s.saveLocked()
			return true
		}
	}
	return false
}

func (s *Store) CountActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.purgeExpiredLocked(now)
	n := 0
	for _, r := range s.rules {
		if r.ActiveAt(now) {
			n++
		}
	}
	return n
}

// Match returns the first currently-active mute rule that matches the alert, or nil.
func (s *Store) Match(a alert.Alert) *Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	for i := range s.rules {
		r := &s.rules[i]
		if !r.ActiveAt(now) {
			continue
		}
		if ruleMatches(r, a) {
			cp := *r
			return &cp
		}
	}
	return nil
}

// MatchIncident returns a mute rule if the incident should be suppressed.
// An incident is suppressed when at least one of its alerts matches an active
// mute rule (matching "alertname" alone suppresses the whole aggregated notice,
// since a single aggregated message cannot partially include/exclude targets).
func (s *Store) MatchIncident(alerts []alert.Alert, source string) *Rule {
	if len(alerts) == 0 {
		return s.Match(alert.Alert{Labels: map[string]string{"alertname": source}})
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()

	for _, a := range alerts {
		for i := range s.rules {
			r := &s.rules[i]
			if !r.ActiveAt(now) {
				continue
			}
			if r.AlertName != "" && (r.AlertName == source || r.AlertName == "*") &&
				r.Instance == "" && r.Hostname == "" && len(r.Matchers) == 0 {
				cp := *r
				return &cp
			}
			if ruleMatches(r, a) {
				cp := *r
				return &cp
			}
		}
	}
	return nil
}

func ruleMatches(r *Rule, a alert.Alert) bool {
	if r.AlertName != "" && r.AlertName != "*" && r.AlertName != a.AlertName() {
		return false
	}
	if r.Instance != "" && r.Instance != a.Instance() {
		return false
	}
	if r.Hostname != "" && r.Hostname != a.Hostname() {
		return false
	}
	for k, v := range r.Matchers {
		if a.Labels[k] != v {
			return false
		}
	}
	return true
}

func (s *Store) purgeExpiredLocked(now time.Time) {
	kept := s.rules[:0]
	changed := false
	for _, r := range s.rules {
		// Keep scheduled + active; drop only fully expired (past ExpiresAt).
		if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
			changed = true
			continue
		}
		kept = append(kept, r)
	}
	s.rules = kept
	if changed {
		_ = s.saveLocked()
	}
}
