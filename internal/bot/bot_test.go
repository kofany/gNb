package bot

import (
	"sync"
	"testing"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/types"
	irc "github.com/kofany/go-ircevo"
)

type stubBot struct {
	nick           string
	connected      bool
	joinedChannels map[string]bool
	sent           []string
	mu             sync.Mutex
}

func (b *stubBot) AttemptNickChange(_ string)               {}
func (b *stubBot) GetCurrentNick() string                   { return b.nick }
func (b *stubBot) IsConnected() bool                        { return b.connected }
func (b *stubBot) IsOnChannel(channel string) bool          { return b.joinedChannels[channel] }
func (b *stubBot) SetOwnerList(_ auth.OwnerList)            {}
func (b *stubBot) SetChannels(_ []string)                   {}
func (b *stubBot) RequestISON(_ []string) ([]string, error) { return nil, nil }
func (b *stubBot) Connect() error                           { return nil }
func (b *stubBot) Quit(_ string)                            {}
func (b *stubBot) Reconnect()                               {}
func (b *stubBot) JoinChannel(_ string)                     {}
func (b *stubBot) PartChannel(_ string)                     {}
func (b *stubBot) ChangeNick(_ string)                      {}
func (b *stubBot) HandleCommands(_ *irc.Event)              {}
func (b *stubBot) SetBotManager(_ types.BotManager)         {}
func (b *stubBot) GetBotManager() types.BotManager          { return nil }
func (b *stubBot) SetNickManager(_ types.NickManager)       {}
func (b *stubBot) GetNickManager() types.NickManager        { return nil }
func (b *stubBot) GetServerName() string                    { return "" }
func (b *stubBot) StartBNC() (int, string, error)           { return 0, "", nil }
func (b *stubBot) StopBNC()                                 {}
func (b *stubBot) SendRaw(_ string)                         {}
func (b *stubBot) RemoveBot()                               {}
func (b *stubBot) SendMessage(target, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, target+":"+message)
}
func (b *stubBot) SetEventSink(_ types.EventSink) {}
func (b *stubBot) GetBotID() string               { return "" }
func (b *stubBot) GetJoinedChannels() []string {
	out := make([]string, 0, len(b.joinedChannels))
	for ch := range b.joinedChannels {
		out = append(out, ch)
	}
	return out
}

type stubNickManager struct {
	targets     []string
	returned    []string
	nickChanges [][2]string
	mu          sync.Mutex
}

func (nm *stubNickManager) RegisterBot(_ types.Bot)                   {}
func (nm *stubNickManager) SetBots(_ []types.Bot)                     {}
func (nm *stubNickManager) AddNick(_ string) error                    { return nil }
func (nm *stubNickManager) RemoveNick(_ string) error                 { return nil }
func (nm *stubNickManager) GetNicks() []string                        { return nil }
func (nm *stubNickManager) MarkNickAsTemporarilyUnavailable(_ string) {}
func (nm *stubNickManager) MarkServerNoLetters(_ string)              {}
func (nm *stubNickManager) Start()                                    {}
func (nm *stubNickManager) Stop()                                     {}
func (nm *stubNickManager) GetNicksToCatch() []string                 { return append([]string(nil), nm.targets...) }
func (nm *stubNickManager) NotifyNickChange(oldNick, newNick string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.nickChanges = append(nm.nickChanges, [2]string{oldNick, newNick})
}
func (nm *stubNickManager) ReturnNickToPool(nick string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.returned = append(nm.returned, nick)
}
func (nm *stubNickManager) SetEventSink(_ types.EventSink) {}

// stubSink is a no-op EventSink satisfying types.EventSink in manager tests.
type stubSink struct{}

func (stubSink) BotConnected(string, string, string)        {}
func (stubSink) BotDisconnected(string, string)             {}
func (stubSink) BotNickChanged(string, string, string)      {}
func (stubSink) BotNickCaptured(string, string, string)     {}
func (stubSink) BotJoinedChannel(string, string)            {}
func (stubSink) BotPartedChannel(string, string)            {}
func (stubSink) BotKicked(string, string, string, string)   {}
func (stubSink) BotBanned(string, int)                      {}
func (stubSink) BotAdded(string, string, int, bool, string) {}
func (stubSink) BotRemoved(string)                          {}
func (stubSink) BotRecovered(string, string)                {}
func (stubSink) NicksChanged([]string)                      {}
func (stubSink) OwnersChanged([]string)                     {}
func (stubSink) BotIRCEvent(string, *irc.Event)             {}
func (stubSink) BotRawOut(string, string)                   {}

var _ types.EventSink = stubSink{}

// recordingSink captures the lifecycle sink calls relevant to the nick-state
// tests below. Other methods inherit stubSink no-ops via embedding.
type recordingSink struct {
	stubSink
	mu                 sync.Mutex
	nickChangedCalls   []nickChangedCall
	nickCapturedCalls  []nickCapturedCall
	nickChangedCounter int
}

type nickChangedCall struct{ botID, oldNick, newNick string }
type nickCapturedCall struct{ botID, nick, kind string }

func (s *recordingSink) BotNickChanged(botID, oldNick, newNick string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nickChangedCalls = append(s.nickChangedCalls, nickChangedCall{botID, oldNick, newNick})
	s.nickChangedCounter++
}

func (s *recordingSink) BotNickCaptured(botID, nick, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nickCapturedCalls = append(s.nickCapturedCalls, nickCapturedCall{botID, nick, kind})
}

func (s *recordingSink) changedSnapshot() []nickChangedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]nickChangedCall(nil), s.nickChangedCalls...)
}

func (s *recordingSink) capturedSnapshot() []nickCapturedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]nickCapturedCall(nil), s.nickCapturedCalls...)
}

var _ types.EventSink = (*recordingSink)(nil)

func TestISONMechanism(t *testing.T) {
	requestID := "test-request-123"
	responseChan := make(chan []string, 1)
	bot := &Bot{
		isonRequests: make(map[string]chan []string),
	}

	bot.isonRequestsMutex.Lock()
	bot.isonRequests[requestID] = responseChan
	bot.isonRequestsMutex.Unlock()

	testResponse := []string{"nick1", "nick2", "nick3"}

	go func() {
		time.Sleep(10 * time.Millisecond)
		bot.isonRequestsMutex.Lock()
		bot.isonRequests[requestID] <- testResponse
		bot.isonRequestsMutex.Unlock()
	}()

	var response []string
	select {
	case response = <-responseChan:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for response")
	}

	if len(response) != len(testResponse) {
		t.Fatalf("expected response length %d, got %d", len(testResponse), len(response))
	}

	for i, nick := range testResponse {
		if response[i] != nick {
			t.Fatalf("expected nick %s at position %d, got %s", nick, i, response[i])
		}
	}

	bot.isonRequestsMutex.Lock()
	delete(bot.isonRequests, requestID)
	remaining := len(bot.isonRequests)
	bot.isonRequestsMutex.Unlock()

	if remaining != 0 {
		t.Fatalf("expected 0 remaining requests, got %d", remaining)
	}
}

func TestSendSingleMsgSkipsDisconnectedBotsAndRoundRobins(t *testing.T) {
	bot1 := &stubBot{nick: "one", connected: true, joinedChannels: map[string]bool{"#chan": true}}
	bot2 := &stubBot{nick: "two", connected: false, joinedChannels: map[string]bool{"#chan": true}}
	bot3 := &stubBot{nick: "three", connected: true, joinedChannels: map[string]bool{"#chan": true}}

	manager := &BotManager{
		bots: []types.Bot{bot1, bot2, bot3},
	}

	manager.SendSingleMsg("#chan", "first")
	manager.SendSingleMsg("#chan", "second")
	manager.SendSingleMsg("#chan", "third")

	if len(bot1.sent) != 2 {
		t.Fatalf("expected bot1 to send 2 messages, got %d", len(bot1.sent))
	}
	if len(bot2.sent) != 0 {
		t.Fatalf("expected disconnected bot2 to send 0 messages, got %d", len(bot2.sent))
	}
	if len(bot3.sent) != 1 {
		t.Fatalf("expected bot3 to send 1 message, got %d", len(bot3.sent))
	}

	if bot1.sent[0] != "#chan:first" || bot3.sent[0] != "#chan:second" || bot1.sent[1] != "#chan:third" {
		t.Fatalf("unexpected send order: bot1=%v bot3=%v", bot1.sent, bot3.sent)
	}
}

func TestSendSingleMsgUsesOnlyBotsJoinedToChannel(t *testing.T) {
	bot1 := &stubBot{nick: "one", connected: true, joinedChannels: map[string]bool{}}
	bot2 := &stubBot{nick: "two", connected: true, joinedChannels: map[string]bool{"#chan": true}}
	bot3 := &stubBot{nick: "three", connected: true, joinedChannels: map[string]bool{}}

	manager := &BotManager{
		bots: []types.Bot{bot1, bot2, bot3},
	}

	manager.SendSingleMsg("#chan", "reply")

	if len(bot1.sent) != 0 || len(bot3.sent) != 0 {
		t.Fatalf("expected only joined bot to send, got bot1=%v bot3=%v", bot1.sent, bot3.sent)
	}
	if len(bot2.sent) != 1 || bot2.sent[0] != "#chan:reply" {
		t.Fatalf("expected joined bot2 to send reply, got %v", bot2.sent)
	}
}

func TestSendSingleMsgSkipsChannelReplyWhenNoBotJoined(t *testing.T) {
	bot1 := &stubBot{nick: "one", connected: true, joinedChannels: map[string]bool{}}
	bot2 := &stubBot{nick: "two", connected: true, joinedChannels: map[string]bool{}}

	manager := &BotManager{
		bots: []types.Bot{bot1, bot2},
	}

	manager.SendSingleMsg("#chan", "reply")

	if len(bot1.sent) != 0 || len(bot2.sent) != 0 {
		t.Fatalf("expected no sends when no bot joined channel, got bot1=%v bot2=%v", bot1.sent, bot2.sent)
	}
}

func TestAttemptNickChangeSkipsWhenBusy(t *testing.T) {
	nm := &stubNickManager{targets: []string{"target"}}
	bot := &Bot{
		CurrentNick: "alpha",
		nickManager: nm,
	}
	bot.nickChangeBusy.Store(true)

	bot.AttemptNickChange("target")

	nm.mu.Lock()
	defer nm.mu.Unlock()
	if len(nm.returned) != 0 {
		t.Fatalf("expected no nick to be returned to pool while bot is busy, got %v", nm.returned)
	}
}

func TestNickManagerStartStopIdempotent(t *testing.T) {
	nm := nickmanager.NewNickManager()
	nm.Start()
	nm.Start()
	time.Sleep(20 * time.Millisecond)
	nm.Stop()
	nm.Stop()
}

func TestTryAcceptSelfNickChangeUpdatesOnMatch(t *testing.T) {
	sink := &recordingSink{}
	nm := &stubNickManager{targets: []string{"target"}}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "alpha",
		nickManager: nm,
		sink:        sink,
	}

	accepted := bot.tryAcceptSelfNickChange("alpha", "beta")
	if !accepted {
		t.Fatalf("expected self-nick change to be accepted")
	}
	if got := bot.GetCurrentNick(); got != "beta" {
		t.Fatalf("expected b.CurrentNick to become beta, got %q", got)
	}
}

func TestTryAcceptSelfNickChangeRejectsForeign(t *testing.T) {
	bot := &Bot{CurrentNick: "alpha"}

	accepted := bot.tryAcceptSelfNickChange("someoneelse", "newnick")
	if accepted {
		t.Fatalf("expected non-self nick event to be rejected")
	}
	if got := bot.GetCurrentNick(); got != "alpha" {
		t.Fatalf("expected b.CurrentNick to stay alpha, got %q", got)
	}
}

func TestTryAcceptSelfNickChangeIgnoresEmptyNew(t *testing.T) {
	bot := &Bot{CurrentNick: "alpha"}

	accepted := bot.tryAcceptSelfNickChange("alpha", "")
	if accepted {
		t.Fatalf("expected empty newNick to be rejected")
	}
	if got := bot.GetCurrentNick(); got != "alpha" {
		t.Fatalf("expected b.CurrentNick to stay alpha, got %q", got)
	}
}

func TestTryAcceptSelfNickChangeRFCEquality(t *testing.T) {
	// RFC 2812 case mapping: '[' '{' are equivalent, ']' '}' are equivalent,
	// '\' '|' are equivalent, '~' '^' are equivalent. The server can echo our
	// nick in either form and must still be recognised as ours.
	cases := []struct {
		stored string
		echoed string
	}{
		{"nick[", "nick{"},
		{"nick]", "nick}"},
		{`nick\`, "nick|"},
		{"nick~", "nick^"},
		{"ALPHA", "alpha"},
	}
	for _, c := range cases {
		t.Run(c.stored+"_vs_"+c.echoed, func(t *testing.T) {
			bot := &Bot{CurrentNick: c.stored}
			if !bot.tryAcceptSelfNickChange(c.echoed, "newone") {
				t.Fatalf("expected RFC nick equality (%s vs %s) to be self", c.stored, c.echoed)
			}
		})
	}
}

func TestEmitSelfNickChangeEmitsBotNickChanged(t *testing.T) {
	sink := &recordingSink{}
	nm := &stubNickManager{targets: []string{"priorityone"}}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "newnick",
		nickManager: nm,
		sink:        sink,
	}

	bot.emitSelfNickChange("oldnick", "newnick")

	changed := sink.changedSnapshot()
	if len(changed) != 1 {
		t.Fatalf("expected 1 BotNickChanged emission, got %d", len(changed))
	}
	if changed[0] != (nickChangedCall{"bot1", "oldnick", "newnick"}) {
		t.Fatalf("unexpected BotNickChanged payload: %+v", changed[0])
	}
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if len(nm.nickChanges) != 1 || nm.nickChanges[0] != [2]string{"oldnick", "newnick"} {
		t.Fatalf("expected NickManager.NotifyNickChange to be called with (oldnick,newnick); got %v", nm.nickChanges)
	}
}

func TestEmitSelfNickChangeReportsLetterCapture(t *testing.T) {
	sink := &recordingSink{}
	nm := &stubNickManager{targets: []string{}}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "a",
		nickManager: nm,
		sink:        sink,
	}

	bot.emitSelfNickChange("longnick", "a")

	captured := sink.capturedSnapshot()
	if len(captured) != 1 || captured[0] != (nickCapturedCall{"bot1", "a", "letter"}) {
		t.Fatalf("expected BotNickCaptured letter for 'a', got %v", captured)
	}
}

func TestEmitSelfNickChangeReportsPriorityCapture(t *testing.T) {
	sink := &recordingSink{}
	nm := &stubNickManager{targets: []string{"hotnick"}}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "hotnick",
		nickManager: nm,
		sink:        sink,
	}

	bot.emitSelfNickChange("randomx", "hotnick")

	captured := sink.capturedSnapshot()
	if len(captured) != 1 || captured[0] != (nickCapturedCall{"bot1", "hotnick", "priority"}) {
		t.Fatalf("expected BotNickCaptured priority for hotnick, got %v", captured)
	}
}

func TestEmitSelfNickChangeNoCaptureForNeutralNick(t *testing.T) {
	sink := &recordingSink{}
	nm := &stubNickManager{targets: []string{"someother"}}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "neutral",
		nickManager: nm,
		sink:        sink,
	}

	bot.emitSelfNickChange("previous", "neutral")

	captured := sink.capturedSnapshot()
	if len(captured) != 0 {
		t.Fatalf("expected no BotNickCaptured for neutral nick, got %v", captured)
	}
}

func TestReconcileSelfNickFromISONFixesDesync(t *testing.T) {
	// Models the IRCnet 436-collision case the user described: server has
	// renamed us to a UID nick, but the bot still thinks it's on the old one.
	// The next ISON 303 reply addresses the bot by its real (server) nick in
	// e.Arguments[0]; reconcile must update local state and emit BotNickChanged.
	sink := &recordingSink{}
	nm := &stubNickManager{targets: []string{}}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "alpha",
		nickManager: nm,
		sink:        sink,
	}

	e := &irc.Event{
		Code:      "303",
		Arguments: []string{"0PNUAABO3", "a b c"},
	}
	bot.reconcileSelfNickFromISON(e)

	if got := bot.GetCurrentNick(); got != "0PNUAABO3" {
		t.Fatalf("expected b.CurrentNick to become 0PNUAABO3, got %q", got)
	}
	changed := sink.changedSnapshot()
	if len(changed) != 1 || changed[0] != (nickChangedCall{"bot1", "alpha", "0PNUAABO3"}) {
		t.Fatalf("expected BotNickChanged(alpha → 0PNUAABO3), got %v", changed)
	}
}

func TestReconcileSelfNickFromISONNoOpWhenInSync(t *testing.T) {
	sink := &recordingSink{}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "alpha",
		sink:        sink,
	}

	e := &irc.Event{
		Code:      "303",
		Arguments: []string{"alpha", "a b c"},
	}
	bot.reconcileSelfNickFromISON(e)

	if got := bot.GetCurrentNick(); got != "alpha" {
		t.Fatalf("expected b.CurrentNick to stay alpha, got %q", got)
	}
	if changed := sink.changedSnapshot(); len(changed) != 0 {
		t.Fatalf("expected no BotNickChanged when ISON target matches, got %v", changed)
	}
}

func TestReconcileSelfNickFromISONNoOpWhenInSyncCaseInsensitive(t *testing.T) {
	// 303 came back with a different case for the same nick — RFC says it
	// is the same nick, so no correction needed and no spurious emission.
	sink := &recordingSink{}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "Alpha",
		sink:        sink,
	}

	e := &irc.Event{
		Code:      "303",
		Arguments: []string{"alpha", "x y z"},
	}
	bot.reconcileSelfNickFromISON(e)

	if changed := sink.changedSnapshot(); len(changed) != 0 {
		t.Fatalf("expected no BotNickChanged on case-only difference, got %v", changed)
	}
}

func TestReconcileSelfNickFromISONHandlesMissingArguments(t *testing.T) {
	sink := &recordingSink{}
	bot := &Bot{
		botID:       "bot1",
		CurrentNick: "alpha",
		sink:        sink,
	}

	e := &irc.Event{Code: "303", Arguments: nil}
	bot.reconcileSelfNickFromISON(e)
	if got := bot.GetCurrentNick(); got != "alpha" {
		t.Fatalf("expected no change with empty arguments, got %q", got)
	}
}

func TestIsTooManyHostConnectionsError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"local variant", "Closing Link: nick[user@host] (Too many host connections (local))", true},
		{"global variant", "Closing Link: nick[user@host] (Too many host connections (global))", true},
		{"bare phrase", "too many host connections", true},
		{"upper case", "TOO MANY HOST CONNECTIONS", true},
		{"k-line is not TMHC", "K-Lined for excessive flooding", false},
		{"ping timeout is not TMHC", "Ping timeout: 240 seconds", false},
		{"empty", "", false},
		{"unrelated 'too many'", "Too many channels", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTooManyHostConnectionsError(tc.msg); got != tc.want {
				t.Errorf("isTooManyHostConnectionsError(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
