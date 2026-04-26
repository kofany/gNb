// Package runtime owns the runtime-mutable state of a gNb node — the things
// the operator can flip on/off through the Panel API while the process is
// running, persisted to disk so they survive restarts.
//
// Currently this is two features:
//   - the channel watchdog (auto-rejoin to configured channels), and
//   - the human-like channel activity scheduler.
//
// State is loaded once at startup, mutated through Set* methods, and observed
// by interested parties (BotManager) through OnChange callbacks. Disk format
// is the same pretty-printed JSON used by configs/owners.json and
// data/nicks.json.
package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// WatchdogConfig describes the auto-rejoin behavior. When Enabled is false
// the bot does not auto-rejoin configured channels at all (no initial join
// after 001, no periodic checker, no post-kick retry). Channel, if
// non-empty, replaces the bot's configured channel list as the watchdog's
// target — the watchdog then guards only that single channel.
type WatchdogConfig struct {
	Enabled bool   `json:"enabled"`
	Channel string `json:"channel"`
}

// HumanlikeConfig describes the human-like activity scheduler. Channels is
// the shared pool every bot picks from independently; per-bot timers and
// picks are randomized inside each bot's runner.
type HumanlikeConfig struct {
	Enabled  bool     `json:"enabled"`
	Channels []string `json:"channels"`
}

// Snapshot is a value-typed copy of the full state suitable for passing to
// observers without exposing internal locks.
type Snapshot struct {
	Watchdog  WatchdogConfig
	Humanlike HumanlikeConfig
}

// fileShape is the on-disk JSON shape. Keep field order/case stable.
type fileShape struct {
	Watchdog  WatchdogConfig  `json:"watchdog"`
	Humanlike HumanlikeConfig `json:"humanlike"`
}

// RuntimeState is the in-memory authoritative copy of the runtime config.
// All access goes through its methods so persistence and observer
// notification are kept in lockstep.
type RuntimeState struct {
	// setMu serializes Set* calls end-to-end (mutate + persist + notify),
	// so concurrent setters always deliver snapshots to observers in the
	// same order they hit mu. mu is the read-side lock used by Get*; it
	// is taken only briefly under setMu, so reads do not block on
	// long-running observers.
	setMu sync.Mutex
	mu    sync.RWMutex

	path      string
	watchdog  WatchdogConfig
	humanlike HumanlikeConfig

	listenersMu sync.Mutex
	listeners   []func(Snapshot)
}

// LoadOrCreate reads path. If it does not exist, an empty default state is
// written to disk and returned. Returns an error only on filesystem or JSON
// failures other than ENOENT.
func LoadOrCreate(path string) (*RuntimeState, error) {
	if path == "" {
		return nil, errors.New("runtime: empty path")
	}
	s := &RuntimeState{path: path, humanlike: HumanlikeConfig{Channels: []string{}}}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if writeErr := s.persistLocked(); writeErr != nil {
			return nil, fmt.Errorf("runtime: write defaults: %w", writeErr)
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runtime: read %s: %w", path, err)
	}
	var f fileShape
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("runtime: parse %s: %w", path, err)
	}
	s.watchdog = f.Watchdog
	s.humanlike = f.Humanlike
	if s.humanlike.Channels == nil {
		s.humanlike.Channels = []string{}
	}
	return s, nil
}

// Get returns a value-typed copy of the full state.
func (s *RuntimeState) Get() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

// Watchdog returns a copy of the watchdog state.
func (s *RuntimeState) Watchdog() WatchdogConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watchdog
}

// Humanlike returns a copy of the human-like state. Channels is a fresh
// non-nil slice so callers may mutate it without racing other readers.
func (s *RuntimeState) Humanlike() HumanlikeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := HumanlikeConfig{
		Enabled:  s.humanlike.Enabled,
		Channels: make([]string, len(s.humanlike.Channels)),
	}
	copy(out.Channels, s.humanlike.Channels)
	return out
}

// SetWatchdog validates and persists a new watchdog config. On success it
// fires registered observers exactly once with the post-update snapshot.
// Concurrent setters are serialized end-to-end via setMu so observers see
// snapshots in the order their setter took effect.
func (s *RuntimeState) SetWatchdog(cfg WatchdogConfig) error {
	cfg.Channel = strings.TrimSpace(cfg.Channel)
	if cfg.Channel != "" {
		if err := validateChannel(cfg.Channel); err != nil {
			return fmt.Errorf("watchdog.channel: %w", err)
		}
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()

	s.mu.Lock()
	s.watchdog = cfg
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.notify(snap)
	return nil
}

// SetHumanlike validates and persists a new human-like config. Channels is
// trimmed, deduplicated case-insensitively (preserving first-seen casing),
// and each entry is validated as a channel name.
func (s *RuntimeState) SetHumanlike(cfg HumanlikeConfig) error {
	clean, err := normalizeChannels(cfg.Channels)
	if err != nil {
		return fmt.Errorf("humanlike.channels: %w", err)
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()

	s.mu.Lock()
	s.humanlike = HumanlikeConfig{Enabled: cfg.Enabled, Channels: clean}
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.notify(snap)
	return nil
}

// OnChange registers an observer. Observers are invoked serially, in
// registration order, after the state mutation has been persisted to disk
// and the internal lock released. They must not block; if they do, all
// later observers and any subsequent Set* call will be delayed.
func (s *RuntimeState) OnChange(fn func(Snapshot)) {
	if fn == nil {
		return
	}
	s.listenersMu.Lock()
	s.listeners = append(s.listeners, fn)
	s.listenersMu.Unlock()
}

// snapshotLocked returns a deep-enough copy under the caller's lock.
// Channels slice is duplicated so observers can store the snapshot.
func (s *RuntimeState) snapshotLocked() Snapshot {
	out := Snapshot{
		Watchdog: s.watchdog,
		Humanlike: HumanlikeConfig{
			Enabled:  s.humanlike.Enabled,
			Channels: make([]string, len(s.humanlike.Channels)),
		},
	}
	copy(out.Humanlike.Channels, s.humanlike.Channels)
	return out
}

// persistLocked writes the current in-memory state to disk atomically. Caller
// must hold s.mu (exclusive). Uses tmp + rename so a crash mid-write leaves
// the previous file intact.
func (s *RuntimeState) persistLocked() error {
	out := fileShape{Watchdog: s.watchdog, Humanlike: s.humanlike}
	if out.Humanlike.Channels == nil {
		out.Humanlike.Channels = []string{}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("runtime: mkdir %s: %w", dir, err)
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("runtime: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("runtime: rename: %w", err)
	}
	return nil
}

func (s *RuntimeState) notify(snap Snapshot) {
	s.listenersMu.Lock()
	listeners := make([]func(Snapshot), len(s.listeners))
	copy(listeners, s.listeners)
	s.listenersMu.Unlock()
	for _, fn := range listeners {
		fn(snap)
	}
}

// validateChannel checks that name is a syntactically reasonable IRC channel.
// We accept the four standard prefixes (#, &, +, !) and reject whitespace
// and overlong names. We do not enforce server-specific rules here.
func validateChannel(name string) error {
	if name == "" {
		return errors.New("empty")
	}
	if len(name) > 50 {
		return fmt.Errorf("too long (%d > 50)", len(name))
	}
	switch name[0] {
	case '#', '&', '+', '!':
	default:
		return fmt.Errorf("must start with #, &, + or ! (got %q)", name[0:1])
	}
	for _, r := range name {
		switch r {
		case ' ', '\t', '\r', '\n', ',', '\x00', '\x07':
			return fmt.Errorf("invalid character %q", r)
		}
	}
	return nil
}

// normalizeChannels trims, validates, and case-insensitively deduplicates.
// Order of first occurrence is preserved.
func normalizeChannels(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if err := validateChannel(c); err != nil {
			return nil, fmt.Errorf("channel %q: %w", c, err)
		}
		key := strings.ToLower(c)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out, nil
}

// SplitChannelList parses a space-separated list of channels into a slice.
// Convenience for the API layer (panel sends one string).
func SplitChannelList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p)
	}
	return out
}
