package bot

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/runtime"
	"github.com/kofany/gNb/internal/util"
)

// humanlikeBounds bracket the random durations for the human-like cycle.
// Production values are 30..180 minutes off-channel and 2..60 minutes
// on-channel; tests pass tiny values to keep runs fast.
type humanlikeBounds struct {
	minOff, maxOff   time.Duration
	minStay, maxStay time.Duration
}

// productionHumanlikeBounds returns the operator-facing values: off-period
// 30 to 180 minutes, stay-period 2 to 60 minutes, with second precision.
func productionHumanlikeBounds() humanlikeBounds {
	return humanlikeBounds{
		minOff:  30 * time.Minute,
		maxOff:  180 * time.Minute,
		minStay: 2 * time.Minute,
		maxStay: 60 * time.Minute,
	}
}

// humanlikeBot is the slice of Bot the runner actually depends on. Pulling it
// behind an interface lets the unit test inject a captured-call fake without
// dragging in the IRC stack.
type humanlikeBot interface {
	currentNick() string
	isConnected() bool
	isOnChannel(channel string) bool
	joinChannel(channel string)
	partChannel(channel string)
}

// humanlikeRunner drives one bot's idle/active cycle. There is at most one
// runner per *Bot. Lifetime is bounded by stop: once closed, the runner
// parts the currently-joined channel (if any) and returns.
type humanlikeRunner struct {
	bot      humanlikeBot
	channels func() []string // returns the current human-like list

	bounds humanlikeBounds
	rng    *rand.Rand

	// Injection seams for tests; nil means use the real time package.
	nowFunc func() time.Time
	after   func(time.Duration) <-chan time.Time

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}

	joinedMu sync.Mutex
	joined   string // current human-like channel; "" if none
}

// newHumanlikeRunner builds a runner with production timing and a fresh
// per-bot RNG. channelsFn is invoked at the top of every cycle to pick up
// list edits from the runtime state.
func newHumanlikeRunner(b humanlikeBot, channelsFn func() []string) *humanlikeRunner {
	return &humanlikeRunner{
		bot:      b,
		channels: channelsFn,
		bounds:   productionHumanlikeBounds(),
		rng:      newPerBotRand(),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// newPerBotRand seeds a math/rand/v2 PCG generator from crypto/rand. Using
// crypto/rand for the seed keeps two bots constructed in the same nanosecond
// from accidentally landing on the same sequence.
func newPerBotRand() *rand.Rand {
	var seed [16]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		// Falling back to time-based seed is fine — collisions are
		// statistically rare and the impact is just less-randomized
		// human-like timing.
		now := uint64(time.Now().UnixNano())
		binary.LittleEndian.PutUint64(seed[0:8], now)
		binary.LittleEndian.PutUint64(seed[8:16], now*2862933555777941757+3037000493)
	}
	s1 := binary.LittleEndian.Uint64(seed[0:8])
	s2 := binary.LittleEndian.Uint64(seed[8:16])
	return rand.New(rand.NewPCG(s1, s2))
}

// start launches the goroutine. Safe to call once per runner.
func (r *humanlikeRunner) start() {
	go r.run()
}

// stopAndPart closes the stop channel and blocks until the runner returns.
// On shutdown the runner parts the currently-joined channel, so the bot
// leaves the channel cleanly when the operator turns the feature off.
// Idempotent: subsequent calls observe the already-closed channels and
// return immediately.
func (r *humanlikeRunner) stopAndPart() {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done
}

// pickDuration returns a random duration in [min, max] with second precision.
// The math/rand/v2 IntN range is half-open, so we use (max-min)/sec + 1 as
// the modulus to make max inclusive.
func (r *humanlikeRunner) pickDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	span := int(max/time.Second-min/time.Second) + 1
	if span <= 1 {
		return min
	}
	return min + time.Duration(r.rng.IntN(span))*time.Second
}

// pickChannel returns a randomly chosen channel from list, excluding any
// the bot is already on. Returns "" if the resulting candidate set is empty.
func (r *humanlikeRunner) pickChannel(list []string) string {
	if len(list) == 0 {
		return ""
	}
	candidates := make([]string, 0, len(list))
	for _, c := range list {
		if !r.bot.isOnChannel(c) {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	return candidates[r.rng.IntN(len(candidates))]
}

// run drives the idle → join → stay → part cycle. Returns when stop is
// closed, after parting the currently-joined channel.
func (r *humanlikeRunner) run() {
	defer close(r.done)
	defer r.partIfJoined()

	for {
		// Off-period (waited *before* the first JOIN, per spec.)
		off := r.pickDuration(r.bounds.minOff, r.bounds.maxOff)
		util.Debug("humanlike: bot %s sleeping %v before next join", r.bot.currentNick(), off)
		if !r.sleep(off) {
			return
		}

		channel := r.pickChannel(r.channels())
		if channel == "" {
			util.Debug("humanlike: bot %s found no eligible channel this cycle", r.bot.currentNick())
			continue
		}
		if !r.bot.isConnected() {
			util.Debug("humanlike: bot %s disconnected, deferring join", r.bot.currentNick())
			continue
		}

		util.Info("humanlike: bot %s joining %s", r.bot.currentNick(), channel)
		r.bot.joinChannel(channel)
		r.setJoined(channel)

		stay := r.pickDuration(r.bounds.minStay, r.bounds.maxStay)
		util.Debug("humanlike: bot %s staying in %s for %v", r.bot.currentNick(), channel, stay)
		if !r.sleep(stay) {
			return
		}

		r.partIfJoined()
	}
}

// sleep waits for d, returning false if stop is closed first.
func (r *humanlikeRunner) sleep(d time.Duration) bool {
	var ch <-chan time.Time
	if r.after != nil {
		ch = r.after(d)
	} else {
		ch = time.After(d)
	}
	select {
	case <-ch:
		return true
	case <-r.stop:
		return false
	}
}

func (r *humanlikeRunner) setJoined(ch string) {
	r.joinedMu.Lock()
	r.joined = ch
	r.joinedMu.Unlock()
}

func (r *humanlikeRunner) partIfJoined() {
	r.joinedMu.Lock()
	ch := r.joined
	r.joined = ""
	r.joinedMu.Unlock()
	if ch == "" {
		return
	}
	if !r.bot.isConnected() {
		return
	}
	// A reconnect between JOIN and PART resets the bot's joined-channels
	// view; a PART for a channel we're no longer on would just trigger
	// 442 NOTONCHANNEL noise. Skip in that case.
	if !r.bot.isOnChannel(ch) {
		return
	}
	util.Info("humanlike: bot %s parting %s", r.bot.currentNick(), ch)
	r.bot.partChannel(ch)
}

// botHumanlikeAdapter adapts *Bot to the humanlikeBot interface used by the
// runner. Kept in this file so the interface and its only adapter live
// together.
type botHumanlikeAdapter struct{ b *Bot }

func (a botHumanlikeAdapter) currentNick() string             { return a.b.GetCurrentNick() }
func (a botHumanlikeAdapter) isConnected() bool               { return a.b.IsConnected() }
func (a botHumanlikeAdapter) isOnChannel(channel string) bool { return a.b.IsOnChannel(channel) }
func (a botHumanlikeAdapter) joinChannel(channel string)      { a.b.JoinChannel(channel) }
func (a botHumanlikeAdapter) partChannel(channel string)      { a.b.PartChannel(channel) }

// runtimeWatchdog returns the watchdog config from the bot's runtime state,
// or the zero (disabled) value if no state is wired. Used in hot paths so
// callers don't need to nil-check.
func (b *Bot) runtimeWatchdog() runtime.WatchdogConfig {
	if b.runtimeState == nil {
		return runtime.WatchdogConfig{}
	}
	return b.runtimeState.Watchdog()
}

// runtimeHumanlike returns the human-like config from the bot's runtime
// state, or the zero (disabled, empty) value if no state is wired.
func (b *Bot) runtimeHumanlike() runtime.HumanlikeConfig {
	if b.runtimeState == nil {
		return runtime.HumanlikeConfig{}
	}
	return b.runtimeState.Humanlike()
}

// watchdogTargets returns the channel list the watchdog should keep this bot
// on. If the runtime state names a single override channel, that's the only
// target; otherwise we fall back to the bot's configured channel list.
func (b *Bot) watchdogTargets() []string {
	wd := b.runtimeWatchdog()
	if wd.Channel != "" {
		return []string{wd.Channel}
	}
	return b.configuredChannels()
}

// applyRuntimeChange reconciles the bot's behavior with a fresh snapshot of
// the runtime state. Called from BotManager whenever the state changes.
//
// Watchdog: turning the feature on starts the periodic checker (if not
// already running) and JOINs any unjoined target. Turning it off stops the
// checker; channels the bot is already on are left alone — the operator
// may want them retained.
//
// Human-like: starting the feature spins up a per-bot runner; stopping it
// asks the runner to part the current channel and exit.
//
// Bots that have been marked gaveUp (RemoveBot in flight) are skipped — we
// don't want to revive a runner moments before the bot leaves the manager.
func (b *Bot) applyRuntimeChange(snap runtime.Snapshot) {
	b.mutex.Lock()
	if b.gaveUp {
		b.mutex.Unlock()
		return
	}
	b.mutex.Unlock()
	b.applyWatchdogChange(snap.Watchdog)
	b.applyHumanlikeChange(snap.Humanlike)
}

func (b *Bot) applyWatchdogChange(wd runtime.WatchdogConfig) {
	if !b.IsConnected() {
		return
	}
	if !wd.Enabled {
		b.stopChannelChecker()
		return
	}
	// Enabled. Make sure the periodic checker is running.
	b.ensureChannelChecker()
	// JOIN any target the bot isn't on.
	for _, ch := range b.watchdogTargets() {
		if !b.IsOnChannel(ch) {
			b.JoinChannel(ch)
		}
	}
}

func (b *Bot) applyHumanlikeChange(hl runtime.HumanlikeConfig) {
	b.mutex.Lock()
	running := b.humanlike != nil
	b.mutex.Unlock()

	switch {
	case hl.Enabled && !running:
		runner := newHumanlikeRunner(botHumanlikeAdapter{b: b}, func() []string {
			return b.runtimeHumanlike().Channels
		})
		b.mutex.Lock()
		b.humanlike = runner
		b.mutex.Unlock()
		runner.start()
	case !hl.Enabled && running:
		b.mutex.Lock()
		runner := b.humanlike
		b.humanlike = nil
		b.mutex.Unlock()
		if runner != nil {
			runner.stopAndPart()
		}
	}
}

// stopChannelChecker stops the watchdog ticker if it is running. Safe to
// call regardless of state.
func (b *Bot) stopChannelChecker() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.channelCheckTicker != nil {
		b.channelCheckTicker.Stop()
		b.channelCheckTicker = nil
	}
	if b.channelCheckerStop != nil {
		close(b.channelCheckerStop)
		b.channelCheckerStop = nil
	}
}

// ensureChannelChecker starts the watchdog ticker if it is not already
// running. Idempotent.
func (b *Bot) ensureChannelChecker() {
	b.mutex.Lock()
	already := b.channelCheckTicker != nil
	b.mutex.Unlock()
	if already {
		return
	}
	b.startChannelChecker()
}
