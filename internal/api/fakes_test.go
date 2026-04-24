package api

import (
	"sync"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/types"
	irc "github.com/kofany/go-ircevo"
)

// fakeBot is a captured-call stand-in for bot.Bot used in API tests.
type fakeBot struct {
	mu sync.Mutex

	botID     string
	nick      string
	server    string
	connected bool
	channels  []string

	sent      []string
	joined    []string
	parted    []string
	quit      string
	reconnect int
	newNick   string
	raw       []string
	bncPort   int
	bncPass   string
}

func (f *fakeBot) AttemptNickChange(string) {}
func (f *fakeBot) GetCurrentNick() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nick
}
func (f *fakeBot) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}
func (f *fakeBot) IsOnChannel(ch string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.channels {
		if c == ch {
			return true
		}
	}
	return false
}
func (f *fakeBot) SetOwnerList(auth.OwnerList)            {}
func (f *fakeBot) SetChannels([]string)                   {}
func (f *fakeBot) RequestISON([]string) ([]string, error) { return nil, nil }
func (f *fakeBot) Connect() error                         { return nil }
func (f *fakeBot) Quit(m string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quit = m
}
func (f *fakeBot) Reconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconnect++
}
func (f *fakeBot) SendMessage(t, m string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, t+": "+m)
}
func (f *fakeBot) JoinChannel(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joined = append(f.joined, c)
}
func (f *fakeBot) PartChannel(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parted = append(f.parted, c)
}
func (f *fakeBot) ChangeNick(n string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newNick = n
}
func (f *fakeBot) HandleCommands(*irc.Event)        {}
func (f *fakeBot) SetBotManager(types.BotManager)   {}
func (f *fakeBot) GetBotManager() types.BotManager  { return nil }
func (f *fakeBot) SetNickManager(types.NickManager) {}
func (f *fakeBot) GetNickManager() types.NickManager {
	return nil
}
func (f *fakeBot) GetServerName() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.server
}
func (f *fakeBot) StartBNC() (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bncPort, f.bncPass, nil
}
func (f *fakeBot) StopBNC() {}
func (f *fakeBot) SendRaw(l string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw = append(f.raw, l)
}
func (f *fakeBot) RemoveBot()                   {}
func (f *fakeBot) SetEventSink(types.EventSink) {}
func (f *fakeBot) GetBotID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.botID
}
func (f *fakeBot) GetJoinedChannels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.channels...)
}

// fakeBotManager is a captured-call BotManager.
type fakeBotManager struct {
	bots        []types.Bot
	owners      []string
	massBlocked map[string]bool
}

func (f *fakeBotManager) StartBots()                             {}
func (f *fakeBotManager) Stop()                                  {}
func (f *fakeBotManager) CanExecuteMassCommand(name string) bool { return !f.massBlocked[name] }
func (f *fakeBotManager) AddOwner(m string) error                { f.owners = append(f.owners, m); return nil }
func (f *fakeBotManager) RemoveOwner(m string) error {
	out := f.owners[:0]
	for _, o := range f.owners {
		if o != m {
			out = append(out, o)
		}
	}
	f.owners = out
	return nil
}
func (f *fakeBotManager) GetOwners() []string                           { return f.owners }
func (f *fakeBotManager) GetBots() []types.Bot                          { return f.bots }
func (f *fakeBotManager) GetNickManager() types.NickManager             { return nil }
func (f *fakeBotManager) GetTotalCreatedBots() int                      { return len(f.bots) }
func (f *fakeBotManager) SetMassCommandCooldown(time.Duration)          {}
func (f *fakeBotManager) GetMassCommandCooldown() time.Duration         { return 0 }
func (f *fakeBotManager) CollectReactions(string, string, func() error) {}
func (f *fakeBotManager) SendSingleMsg(string, string)                  {}
func (f *fakeBotManager) SetEventSink(types.EventSink)                  {}

type fakeNickManager struct {
	mu    sync.Mutex
	nicks []string
}

func (f *fakeNickManager) RegisterBot(types.Bot)     {}
func (f *fakeNickManager) ReturnNickToPool(string)   {}
func (f *fakeNickManager) SetBots([]types.Bot)       {}
func (f *fakeNickManager) GetNicksToCatch() []string { return f.nicks }
func (f *fakeNickManager) AddNick(n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nicks = append(f.nicks, n)
	return nil
}
func (f *fakeNickManager) RemoveNick(n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.nicks[:0]
	for _, x := range f.nicks {
		if x != n {
			out = append(out, x)
		}
	}
	f.nicks = out
	return nil
}
func (f *fakeNickManager) GetNicks() []string                      { return f.nicks }
func (f *fakeNickManager) MarkNickAsTemporarilyUnavailable(string) {}
func (f *fakeNickManager) NotifyNickChange(string, string)         {}
func (f *fakeNickManager) MarkServerNoLetters(string)              {}
func (f *fakeNickManager) Start()                                  {}
func (f *fakeNickManager) Stop()                                   {}
func (f *fakeNickManager) SetEventSink(types.EventSink)            {}
