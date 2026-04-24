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

type stubNickManager struct {
	targets  []string
	returned []string
	mu       sync.Mutex
}

func (nm *stubNickManager) RegisterBot(_ types.Bot)                   {}
func (nm *stubNickManager) SetBots(_ []types.Bot)                     {}
func (nm *stubNickManager) AddNick(_ string) error                    { return nil }
func (nm *stubNickManager) RemoveNick(_ string) error                 { return nil }
func (nm *stubNickManager) GetNicks() []string                        { return nil }
func (nm *stubNickManager) MarkNickAsTemporarilyUnavailable(_ string) {}
func (nm *stubNickManager) NotifyNickChange(_, _ string)              {}
func (nm *stubNickManager) MarkServerNoLetters(_ string)              {}
func (nm *stubNickManager) Start()                                    {}
func (nm *stubNickManager) Stop()                                     {}
func (nm *stubNickManager) GetNicksToCatch() []string                 { return append([]string(nil), nm.targets...) }
func (nm *stubNickManager) ReturnNickToPool(nick string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.returned = append(nm.returned, nick)
}
func (nm *stubNickManager) SetEventSink(_ types.EventSink) {}

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
