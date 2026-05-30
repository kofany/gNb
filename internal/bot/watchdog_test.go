package bot

import (
	"testing"
	"time"
)

// TestLivenessDecision pins the pure timing logic of the watchdog: the
// probe-then-kill cycle that catches a wedged socket the library would
// otherwise sit on for ~16 min before emitting DISCONNECTED.
func TestLivenessDecision(t *testing.T) {
	idle := 6 * time.Minute
	grace := 30 * time.Second
	base := time.Unix(1_700_000_000, 0)

	cases := []struct {
		name      string
		now       time.Time
		lastEvent time.Time
		probeAt   time.Time
		want      wdAction
	}{
		{"fresh, no probe", base, base.Add(-1 * time.Minute), time.Time{}, wdNone},
		{"idle past threshold, no probe", base, base.Add(-7 * time.Minute), time.Time{}, wdProbe},
		{"probe pending within grace", base, base.Add(-7 * time.Minute), base.Add(-10 * time.Second), wdNone},
		{"probe answered (event after probe)", base, base.Add(-2 * time.Second), base.Add(-10 * time.Second), wdClear},
		{"probe unanswered past grace", base, base.Add(-7 * time.Minute), base.Add(-31 * time.Second), wdKill},
		{"exactly at idle is not yet probe", base, base.Add(-6 * time.Minute), time.Time{}, wdNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := livenessDecision(c.now, c.lastEvent, c.probeAt, idle, grace); got != c.want {
				t.Fatalf("livenessDecision = %v, want %v", got, c.want)
			}
		})
	}
}

// TestApplyLivenessProbeThenKill: an idle bot is probed once, then declared
// dead when the probe goes unanswered past the grace window.
func TestApplyLivenessProbeThenKill(t *testing.T) {
	idle := 6 * time.Minute
	grace := 30 * time.Second
	b := &Bot{CurrentNick: "n"}
	now := time.Unix(1_700_000_000, 0)
	b.lastEventAt.Store(now.Add(-7 * time.Minute).UnixNano())

	// First sweep: idle → probe.
	if act := b.applyLiveness(now, idle, grace); act != wdProbe {
		t.Fatalf("first sweep = %v, want wdProbe", act)
	}
	if b.wdProbeAt.Load() == 0 {
		t.Fatal("expected wdProbeAt to be set after probe")
	}

	// Probe unanswered, past grace → kill.
	later := now.Add(31 * time.Second)
	if act := b.applyLiveness(later, idle, grace); act != wdKill {
		t.Fatalf("second sweep = %v, want wdKill", act)
	}
}

// TestApplyLivenessProbeThenRecover: a probe is cleared when an inbound line
// arrives after it (the socket is alive, just quiet).
func TestApplyLivenessProbeThenRecover(t *testing.T) {
	idle := 6 * time.Minute
	grace := 30 * time.Second
	b := &Bot{CurrentNick: "n"}
	now := time.Unix(1_700_000_000, 0)
	b.lastEventAt.Store(now.Add(-7 * time.Minute).UnixNano())

	if act := b.applyLiveness(now, idle, grace); act != wdProbe {
		t.Fatalf("first sweep = %v, want wdProbe", act)
	}
	// An inbound line arrives (PONG) after the probe.
	b.lastEventAt.Store(now.Add(1 * time.Second).UnixNano())
	if act := b.applyLiveness(now.Add(2*time.Second), idle, grace); act != wdClear {
		t.Fatalf("recovery sweep = %v, want wdClear", act)
	}
	if b.wdProbeAt.Load() != 0 {
		t.Fatal("expected wdProbeAt cleared after recovery")
	}
}
