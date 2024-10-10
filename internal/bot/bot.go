// File: internal/bot/bot.go

package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevent"
)

// Upewnij się, że Bot implementuje types.Bot
var _ types.Bot = (*Bot)(nil)

// Bot reprezentuje pojedynczego bota IRC
type Bot struct {
	Config          *config.BotConfig
	GlobalConfig    *config.GlobalConfig
	Connection      *irc.Connection
	CurrentNick     string
	Username        string
	Realname        string
	isConnected     bool
	owners          auth.OwnerList
	channels        []string
	nickManager     types.NickManager
	isReconnecting  bool
	lastConnectTime time.Time
	connected       chan struct{}
	botManager      types.BotManager
	gaveUp          bool
}

func NewBot(cfg *config.BotConfig, globalConfig *config.GlobalConfig, nm types.NickManager, bm types.BotManager) *Bot {
	nick, err := util.GenerateRandomNick(globalConfig.NickAPI.URL, globalConfig.NickAPI.MaxWordLength, globalConfig.NickAPI.Timeout)
	if err != nil {
		nick = util.GenerateFallbackNick()
	}

	ident, err := util.GenerateRandomNick(globalConfig.NickAPI.URL, globalConfig.NickAPI.MaxWordLength, globalConfig.NickAPI.Timeout)
	if err != nil {
		ident = "botuser"
	}

	realname, err := util.GenerateRandomNick(globalConfig.NickAPI.URL, globalConfig.NickAPI.MaxWordLength, globalConfig.NickAPI.Timeout)
	if err != nil {
		realname = "Nick Catcher Bot"
	}

	bot := &Bot{
		Config:       cfg,
		GlobalConfig: globalConfig,
		CurrentNick:  nick,
		Username:     ident,
		Realname:     realname,
		isConnected:  false,
		nickManager:  nm,
		connected:    make(chan struct{}),
		botManager:   bm,
	}

	nm.RegisterBot(bot)
	return bot
}

// IsConnected zwraca status połączenia bota
func (b *Bot) IsConnected() bool {
	return b.isConnected
}

// Connect nawiązuje połączenie z serwerem IRC z logiką ponownych prób
func (b *Bot) Connect() error {
	return b.connectWithRetry()
}

// connectWithRetry próbuje połączyć się z serwerem z określoną liczbą ponownych prób
func (b *Bot) connectWithRetry() error {
	maxRetries := b.GlobalConfig.ReconnectRetries
	retryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	for attempts := 0; attempts < maxRetries; attempts++ {
		b.Connection = irc.IRC(b.CurrentNick, b.Username, b.Config.Vhost)
		b.Connection.VerboseCallbackHandler = false
		b.Connection.Debug = false
		b.Connection.UseTLS = b.Config.SSL
		b.Connection.RealName = b.Realname

		// Inicjalizacja kanału connected
		b.connected = make(chan struct{})

		// Dodaj callbacki
		b.addCallbacks()

		util.Info("Bot %s próbuje połączyć się z %s", b.CurrentNick, b.Config.ServerAddress())
		err := b.Connection.Connect(b.Config.ServerAddress())
		if err != nil {
			util.Error("Próba %d: Nie udało się połączyć bota %s: %v", attempts+1, b.CurrentNick, err)
			time.Sleep(retryInterval)
			continue
		}

		go b.Connection.Loop()

		// Czekaj na potwierdzenie połączenia lub timeout
		select {
		case <-b.connected:
			// Połączenie nawiązane
			util.Info("Bot %s pomyślnie połączony", b.CurrentNick)
			return nil
		case <-time.After(30 * time.Second):
			util.Warning("Bot %s nie otrzymał potwierdzenia połączenia w ciągu 30 sekund", b.CurrentNick)
			b.Connection.Quit()
		}
	}

	return fmt.Errorf("bot %s nie mógł się połączyć po %d próbach", b.CurrentNick, maxRetries)
}

// addCallbacks dodaje niezbędne callbacki do połączenia IRC
func (b *Bot) addCallbacks() {
	// Callback dla pomyślnego połączenia
	b.Connection.AddCallback("001", func(e *irc.Event) {
		b.isConnected = true
		b.gaveUp = false // Resetowanie flagi po pomyślnym połączeniu
		b.lastConnectTime = time.Now()
		util.Info("Bot %s połączony z %s jako %s", b.CurrentNick, b.Config.Server, b.CurrentNick)
		// Dołącz do kanałów
		for _, channel := range b.channels {
			b.JoinChannel(channel)
		}
		close(b.connected) // Sygnał, że połączenie zostało nawiązane
	})

	// Callback dla zmian nicka
	b.Connection.AddCallback("NICK", func(e *irc.Event) {
		if e.Nick == b.CurrentNick {
			b.CurrentNick = e.Message()
			util.Info("Bot %s zmienił nick na %s", e.Nick, b.CurrentNick)
		}
	})

	// Callback dla odpowiedzi ISON
	b.Connection.AddCallback("303", b.handleISONResponse)

	// Callback dla wiadomości prywatnych i publicznych
	b.Connection.AddCallback("PRIVMSG", b.handlePrivMsg)

	// Callback dla rozłączenia
	b.Connection.AddCallback("DISCONNECTED", func(e *irc.Event) {
		util.Warning("Bot %s rozłączony z serwerem", b.CurrentNick)
		b.isConnected = false
		if !b.isReconnecting && !b.gaveUp {
			go b.handleReconnect()
		} else if b.gaveUp {
			util.Info("Bot %s zrezygnował z ponownych prób połączenia", b.CurrentNick)
		}
	})
}

// handleReconnect obsługuje próby ponownego połączenia po rozłączeniu
func (b *Bot) handleReconnect() {
	b.isReconnecting = true
	defer func() { b.isReconnecting = false }()

	maxRetries := b.GlobalConfig.ReconnectRetries
	retryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	for attempts := 0; attempts < maxRetries; attempts++ {
		util.Info("Bot %s próbuje ponownie się połączyć (próba %d/%d)", b.CurrentNick, attempts+1, maxRetries)
		err := b.connectWithRetry()
		if err == nil {
			util.Info("Bot %s ponownie połączony", b.CurrentNick)
			return
		}
		util.Error("Próba %d nie powiodła się: %v", attempts+1, err)
		time.Sleep(retryInterval)
	}

	util.Error("Bot %s nie mógł ponownie się połączyć po %d próbach", b.CurrentNick, maxRetries)
	b.gaveUp = true
}

// SendISON wysyła komendę ISON z listą nicków do sprawdzenia
func (b *Bot) SendISON(nicks []string) {
	if b.IsConnected() {
		command := fmt.Sprintf("ISON %s", strings.Join(nicks, " "))
		util.Debug("Bot %s wysyła komendę ISON: %s", b.CurrentNick, command)
		b.Connection.SendRaw(command)
	} else {
		util.Debug("Bot %s nie jest połączony; nie można wysłać ISON", b.CurrentNick)
	}
}

// ChangeNick próbuje zmienić nick bota na nowy
func (b *Bot) ChangeNick(newNick string) {
	if b.IsConnected() {
		util.Info("Bot %s próbuje zmienić nick na %s", b.CurrentNick, newNick)
		b.Connection.Nick(newNick)
	} else {
		util.Debug("Bot %s nie jest połączony; nie można zmienić nicka", b.CurrentNick)
	}
}

// JoinChannel dołącza do określonego kanału
func (b *Bot) JoinChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s dołącza do kanału %s", b.CurrentNick, channel)
		b.Connection.Join(channel)
	} else {
		util.Debug("Bot %s nie jest połączony; nie można dołączyć do kanału %s", b.CurrentNick, channel)
	}
}

// PartChannel opuszcza określony kanał
func (b *Bot) PartChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s opuszcza kanał %s", b.CurrentNick, channel)
		b.Connection.Part(channel)
	} else {
		util.Debug("Bot %s nie jest połączony; nie można opuścić kanału %s", b.CurrentNick, channel)
	}
}

// Reconnect rozłącza i ponownie łączy bota z serwerem IRC
func (b *Bot) Reconnect() {
	if b.IsConnected() {
		b.Quit("Ponowne łączenie")
	}
	time.Sleep(5 * time.Second)
	err := b.Connect()
	if err != nil {
		util.Error("Nie udało się ponownie połączyć bota %s: %v", b.CurrentNick, err)
	}
}

// SendMessage wysyła wiadomość do określonego celu (kanał lub użytkownik)
func (b *Bot) SendMessage(target, message string) {
	if b.IsConnected() {
		util.Debug("Bot %s wysyła wiadomość do %s: %s", b.CurrentNick, target, message)
		b.Connection.Privmsg(target, message)
	} else {
		util.Debug("Bot %s nie jest połączony; nie można wysłać wiadomości do %s", b.CurrentNick, target)
	}
}

// SendNotice wysyła notice do określonego celu (kanał lub użytkownik)
func (b *Bot) SendNotice(target, message string) {
	if b.IsConnected() {
		util.Debug("Bot %s wysyła notice do %s: %s", b.CurrentNick, target, message)
		b.Connection.Notice(target, message)
	} else {
		util.Debug("Bot %s nie jest połączony; nie można wysłać notice do %s", b.CurrentNick, target)
	}
}

// Quit rozłącza bota z serwerem IRC
func (b *Bot) Quit(message string) {
	if b.IsConnected() {
		util.Info("Bot %s rozłącza się: %s", b.CurrentNick, message)
		b.Connection.QuitMessage = message
		b.Connection.Quit()
		b.isConnected = false
	}
}

func (b *Bot) handleISONResponse(e *irc.Event) {
	isonResponse := strings.Fields(e.Message())
	util.Debug("Bot %s otrzymał odpowiedź ISON: %v", b.CurrentNick, isonResponse)
	b.nickManager.HandleISONResponse(isonResponse)
}

// AttemptNickChange próbuje zmienić nick na dostępny
func (b *Bot) AttemptNickChange(nick string) {
	util.Debug("Bot %s otrzymał prośbę o zmianę nicka na %s", b.CurrentNick, nick)
	if b.shouldChangeNick(nick) {
		util.Info("Bot %s próbuje zmienić nick na %s", b.CurrentNick, nick)
		b.ChangeNick(nick)
	} else {
		util.Debug("Bot %s zdecydował nie zmieniać nicka na %s", b.CurrentNick, nick)
		b.nickManager.ReturnNickToPool(nick)
	}
}

func (b *Bot) shouldChangeNick(nick string) bool {
	return b.CurrentNick != nick
}

// handlePrivMsg obsługuje wiadomości prywatne i publiczne oraz komendy właścicieli
func (b *Bot) handlePrivMsg(e *irc.Event) {
	message := e.Message()
	sender := e.Nick

	// Sprawdź, czy wiadomość zaczyna się od któregoś z prefiksów komend
	if !startsWithAny(message, b.GlobalConfig.CommandPrefixes) {
		return
	}

	// Sprawdź, czy nadawca jest właścicielem
	if !auth.IsOwner(e, b.owners) {
		return
	}

	// Przed przetworzeniem komendy, sprawdź, czy ten bot powinien ją obsłużyć
	if !b.shouldHandleCommand() {
		return
	}

	// Parsuj komendę
	commandLine := strings.TrimLeft(message, strings.Join(b.GlobalConfig.CommandPrefixes, ""))
	args := strings.Fields(commandLine)

	if len(args) == 0 {
		return
	}

	// Obsługa komend
	switch strings.ToLower(args[0]) {
	case "quit":
		b.Quit("Komenda od właściciela")
	case "say":
		if len(args) >= 3 {
			targetChannel := args[1]
			msg := strings.Join(args[2:], " ")
			b.SendMessage(targetChannel, msg)
		}
	case "join":
		if len(args) >= 2 {
			channel := args[1]
			b.JoinChannel(channel)
		}
	case "part":
		if len(args) >= 2 {
			channel := args[1]
			b.PartChannel(channel)
		}
	case "reconnect":
		b.Reconnect()
	default:
		b.SendNotice(sender, "Nieznana komenda")
	}
}

// shouldHandleCommand określa, czy ten bot powinien obsłużyć komendę
func (b *Bot) shouldHandleCommand() bool {
	return b.botManager.ShouldHandleCommand(b)
}

// SetOwnerList ustawia listę właścicieli dla bota
func (b *Bot) SetOwnerList(owners auth.OwnerList) {
	b.owners = owners
	util.Debug("Bot %s ustawił właścicieli: %v", b.CurrentNick, owners)
}

// SetChannels ustawia listę kanałów, do których bot ma dołączyć
func (b *Bot) SetChannels(channels []string) {
	b.channels = channels
	util.Debug("Bot %s ustawił kanały: %v", b.CurrentNick, channels)
}

// GetCurrentNick zwraca aktualny nick bota
func (b *Bot) GetCurrentNick() string {
	return b.CurrentNick
}

// Funkcja pomocnicza sprawdzająca, czy string zaczyna się od któregoś z podanych prefiksów
func startsWithAny(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
