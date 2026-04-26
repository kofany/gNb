package bot

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// fakeHumanlikeBot is a captured-call humanlikeBot. Reads/writes are
// serialized through mu.
type fakeHumanlikeBot struct {
	mu         sync.Mutex
	connected  bool
	onChannels map[string]bool
	joinCalls  []string
	partCalls  []string
}

func newFakeHumanlikeBot(connected bool, on ...string) *fakeHumanlikeBot {
	f := &fakeHumanlikeBot{connected: connected, onChannels: make(map[string]bool)}
	for _, c := range on {
		f.onChannels[c] = true
	}
	return f
}

func (f *fakeHumanlikeBot) currentNick() string { return "fake" }
func (f *fakeHumanlikeBot) isConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}
func (f *fakeHumanlikeBot) isOnChannel(c string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.onChannels[c]
}
func (f *fakeHumanlikeBot) joinChannel(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joinCalls = append(f.joinCalls, c)
	f.onChannels[c] = true
}
func (f *fakeHumanlikeBot) partChannel(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partCalls = append(f.partCalls, c)
	delete(f.onChannels, c)
}

// fakeClock issues channels we trigger by hand. A test calls fire() to make
// the next sleep return immediately; if the test never calls fire(), the
// runner is parked on stop instead.
type fakeClock struct {
	mu    sync.Mutex
	chans []chan time.Time
}

func (c *fakeClock) after(_ time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.chans = append(c.chans, ch)
	c.mu.Unlock()
	return ch
}

// fire releases the next pending sleep. Returns true if it released one,
// false if there was nothing to release.
func (c *fakeClock) fire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, ch := range c.chans {
		if ch == nil {
			continue
		}
		ch <- time.Now()
		c.chans[i] = nil
		return true
	}
	return false
}

// waitForFire blocks until at least n channels exist that haven't been
// fired yet, or the deadline elapses. Lets tests synchronize to "the
// runner has parked on time.After" without sleeping arbitrary durations.
func (c *fakeClock) waitForPending(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		pending := 0
		for _, ch := range c.chans {
			if ch != nil {
				pending++
			}
		}
		c.mu.Unlock()
		if pending >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending sleeps", n)
}

func newTestRunner(b humanlikeBot, channels []string) (*humanlikeRunner, *fakeClock) {
	c := &fakeClock{}
	r := &humanlikeRunner{
		bot:      b,
		channels: func() []string { return channels },
		bounds: humanlikeBounds{
			minOff: 1, maxOff: 1, minStay: 1, maxStay: 1, // any non-zero; clock decides
		},
		rng:   rand.New(rand.NewPCG(1, 2)), // deterministic for tests
		after: c.after,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	return r, c
}

func TestHumanlike_BasicCycle_JoinThenPart(t *testing.T) {
	fb := newFakeHumanlikeBot(true)
	r, clk := newTestRunner(fb, []string{"#alpha", "#beta"})
	r.start()
	t.Cleanup(func() { r.stopAndPart() })

	clk.waitForPending(t, 1) // sleeping off-period
	clk.fire()               // release: now JOIN, then sleep stay-period
	clk.waitForPending(t, 1)

	fb.mu.Lock()
	if len(fb.joinCalls) != 1 {
		t.Fatalf("expected 1 join, got %v", fb.joinCalls)
	}
	joined := fb.joinCalls[0]
	fb.mu.Unlock()

	clk.fire() // release stay → PART
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fb.mu.Lock()
		n := len(fb.partCalls)
		fb.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.partCalls) != 1 || fb.partCalls[0] != joined {
		t.Fatalf("expected part of %s, got %v", joined, fb.partCalls)
	}
}

func TestHumanlike_EmptyList_NoJoin(t *testing.T) {
	fb := newFakeHumanlikeBot(true)
	r, clk := newTestRunner(fb, []string{})
	r.start()
	t.Cleanup(func() { r.stopAndPart() })

	clk.waitForPending(t, 1)
	clk.fire() // off → pickChannel returns "" → continue → next off
	clk.waitForPending(t, 1)

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.joinCalls) != 0 {
		t.Fatalf("expected no join with empty list, got %v", fb.joinCalls)
	}
}

func TestHumanlike_AllAlreadyJoined_Skips(t *testing.T) {
	fb := newFakeHumanlikeBot(true, "#x", "#y")
	r, clk := newTestRunner(fb, []string{"#x", "#y"})
	r.start()
	t.Cleanup(func() { r.stopAndPart() })

	clk.waitForPending(t, 1)
	clk.fire() // off → pickChannel returns "" (all on) → continue
	clk.waitForPending(t, 1)

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.joinCalls) != 0 {
		t.Fatalf("expected no join when already on every channel, got %v", fb.joinCalls)
	}
}

func TestHumanlike_DisconnectedAtJoin_DefersJoin(t *testing.T) {
	fb := newFakeHumanlikeBot(false)
	r, clk := newTestRunner(fb, []string{"#a"})
	r.start()
	t.Cleanup(func() { r.stopAndPart() })

	clk.waitForPending(t, 1)
	clk.fire()
	clk.waitForPending(t, 1) // back to next off-period

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.joinCalls) != 0 {
		t.Fatalf("disconnected bot must not JOIN, got %v", fb.joinCalls)
	}
}

func TestHumanlike_StopMidStay_PartsCurrentChannel(t *testing.T) {
	fb := newFakeHumanlikeBot(true)
	r, clk := newTestRunner(fb, []string{"#a"})
	r.start()

	clk.waitForPending(t, 1)
	clk.fire() // off → JOIN → sleep stay
	clk.waitForPending(t, 1)

	// Before stay expires, ask the runner to stop. It should PART before
	// returning.
	r.stopAndPart()

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.joinCalls) != 1 || fb.joinCalls[0] != "#a" {
		t.Fatalf("expected join of #a, got %v", fb.joinCalls)
	}
	if len(fb.partCalls) != 1 || fb.partCalls[0] != "#a" {
		t.Fatalf("expected part on stop, got %v", fb.partCalls)
	}
}

func TestHumanlike_PickDuration_RangeAndPrecision(t *testing.T) {
	r := &humanlikeRunner{
		rng: rand.New(rand.NewPCG(1, 2)),
	}
	min := 30 * time.Second
	max := 40 * time.Second
	for i := 0; i < 256; i++ {
		d := r.pickDuration(min, max)
		if d < min || d > max {
			t.Fatalf("iteration %d: %v out of range [%v, %v]", i, d, min, max)
		}
		if d%time.Second != 0 {
			t.Fatalf("iteration %d: %v has sub-second precision", i, d)
		}
	}
}

func TestHumanlike_PickDuration_DegenerateRange(t *testing.T) {
	r := &humanlikeRunner{rng: rand.New(rand.NewPCG(1, 2))}
	if got := r.pickDuration(5*time.Second, 5*time.Second); got != 5*time.Second {
		t.Fatalf("min==max should return min, got %v", got)
	}
	if got := r.pickDuration(10*time.Second, 5*time.Second); got != 10*time.Second {
		t.Fatalf("max<min should return min, got %v", got)
	}
}
