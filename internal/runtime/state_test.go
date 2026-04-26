package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "runtime.json")
}

func TestLoadOrCreate_MissingFile_WritesDefaults(t *testing.T) {
	p := tempPath(t)
	s, err := LoadOrCreate(p)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if s.Watchdog().Enabled || s.Watchdog().Channel != "" {
		t.Fatalf("watchdog default not zero: %+v", s.Watchdog())
	}
	if s.Humanlike().Enabled {
		t.Fatalf("humanlike default not disabled")
	}
	if got := s.Humanlike().Channels; got == nil || len(got) != 0 {
		t.Fatalf("humanlike default channels: %+v (want empty slice)", got)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	var f fileShape
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("file not valid json: %v", err)
	}
	if f.Humanlike.Channels == nil {
		t.Fatalf("disk channels should serialize as [], not null")
	}
}

func TestLoadOrCreate_RoundTrip(t *testing.T) {
	p := tempPath(t)
	s, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetWatchdog(WatchdogConfig{Enabled: true, Channel: "#foo"}); err != nil {
		t.Fatalf("SetWatchdog: %v", err)
	}
	if err := s.SetHumanlike(HumanlikeConfig{Enabled: true, Channels: []string{"#a", "#b"}}); err != nil {
		t.Fatalf("SetHumanlike: %v", err)
	}

	s2, err := LoadOrCreate(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := s2.Watchdog(); got.Enabled != true || got.Channel != "#foo" {
		t.Fatalf("watchdog reload: %+v", got)
	}
	if got := s2.Humanlike(); !got.Enabled || len(got.Channels) != 2 || got.Channels[0] != "#a" || got.Channels[1] != "#b" {
		t.Fatalf("humanlike reload: %+v", got)
	}
}

func TestSetWatchdog_Validation(t *testing.T) {
	s, err := LoadOrCreate(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		channel string
		ok      bool
	}{
		{"empty ok", "", true},
		{"hash ok", "#foo", true},
		{"amp ok", "&foo", true},
		{"plus ok", "+foo", true},
		{"bang ok", "!foo", true},
		{"bare reject", "foo", false},
		{"space reject", "#foo bar", false},
		{"comma reject", "#foo,#bar", false},
		{"too long reject", "#" + string(make([]byte, 60)), false},
	}
	for _, tc := range cases {
		err := s.SetWatchdog(WatchdogConfig{Enabled: true, Channel: tc.channel})
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected err: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: expected validation error, got nil", tc.name)
		}
	}
}

func TestSetHumanlike_NormalizesChannels(t *testing.T) {
	s, err := LoadOrCreate(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetHumanlike(HumanlikeConfig{Enabled: false, Channels: []string{"#A", "#a", " #b ", "", "#B"}}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := s.Humanlike().Channels
	if len(got) != 2 || got[0] != "#A" || got[1] != "#b" {
		t.Fatalf("normalize: %+v (want [#A #b])", got)
	}
}

func TestSetHumanlike_RejectsBadChannel(t *testing.T) {
	s, err := LoadOrCreate(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetHumanlike(HumanlikeConfig{Enabled: true, Channels: []string{"#ok", "bad"}}); err == nil {
		t.Fatalf("expected validation error for bare channel name")
	}
}

func TestOnChange_FiresAfterPersist(t *testing.T) {
	s, err := LoadOrCreate(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu       sync.Mutex
		seen     []Snapshot
		barrier  sync.WaitGroup
		fireOnce sync.Once
	)
	barrier.Add(1)
	s.OnChange(func(snap Snapshot) {
		mu.Lock()
		seen = append(seen, snap)
		mu.Unlock()
		fireOnce.Do(func() { barrier.Done() })
	})
	if err := s.SetWatchdog(WatchdogConfig{Enabled: true, Channel: "#x"}); err != nil {
		t.Fatal(err)
	}
	barrier.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("listener fires: got %d, want 1", len(seen))
	}
	if seen[0].Watchdog.Channel != "#x" {
		t.Fatalf("snapshot stale: %+v", seen[0])
	}
}

func TestPersist_AtomicityNoTmpLeftover(t *testing.T) {
	p := tempPath(t)
	s, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.SetWatchdog(WatchdogConfig{Enabled: true, Channel: "#round"}); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Dir(p)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestObserversSerializedUnderContention(t *testing.T) {
	// Two goroutines hammer SetWatchdog. Each call increments a per-snapshot
	// counter. Because setMu serializes (mutate + notify), the observer
	// must see snapshots in the same order their setter took the lock —
	// the on-disk file's final value must match the *last* delivered
	// snapshot.
	s, err := LoadOrCreate(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	const iterations = 200
	var mu sync.Mutex
	last := WatchdogConfig{}
	s.OnChange(func(snap Snapshot) {
		mu.Lock()
		last = snap.Watchdog
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = s.SetWatchdog(WatchdogConfig{
					Enabled: i%2 == 0,
					Channel: "#g" + string(rune('a'+g)),
				})
			}
		}(g)
	}
	wg.Wait()

	// The last-delivered snapshot must equal the persisted state — if the
	// observer order had drifted, we'd see them disagree.
	mu.Lock()
	got := last
	mu.Unlock()
	persisted := s.Watchdog()
	if got != persisted {
		t.Fatalf("last observer snapshot %+v != persisted %+v", got, persisted)
	}
}

func TestSplitChannelList(t *testing.T) {
	cases := map[string][]string{
		"":          {},
		" ":         {},
		"#a":        {"#a"},
		"#a #b  #c": {"#a", "#b", "#c"},
		"\t#a\n#b ": {"#a", "#b"},
	}
	for in, want := range cases {
		got := SplitChannelList(in)
		if len(got) != len(want) {
			t.Errorf("split %q: len %d, want %d (got=%v)", in, len(got), len(want), got)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("split %q[%d]: %q want %q", in, i, got[i], want[i])
			}
		}
	}
}
