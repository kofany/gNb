// Plik internal/irc/client.go

package irc

import (
	"fmt"
	"strings"

	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevent"
)

// Client jest abstrakcją połączenia IRC, ułatwiającą interakcję z serwerem IRC.
type Client struct {
	Connection  *irc.Connection
	Server      string
	Port        int
	Nick        string
	Username    string
	Realname    string
	Vhost       string
	SSL         bool
	IsConnected bool
}

// NewClient tworzy nową instancję klienta IRC.
func NewClient(server string, port int, nick, username, realname, vhost string, ssl bool) *Client {
	return &Client{
		Server:   server,
		Port:     port,
		Nick:     nick,
		Username: username,
		Realname: realname,
		Vhost:    vhost,
		SSL:      ssl,
	}
}

// Connect nawiązuje połączenie z serwerem IRC i inicjuje pętlę zdarzeń.
func (c *Client) Connect() error {
	c.Connection = irc.IRC(c.Nick, c.Username, c.Vhost)
	c.Connection.VerboseCallbackHandler = false
	c.Connection.Debug = false
	c.Connection.UseTLS = c.SSL
	c.Connection.RealName = c.Realname

	err := c.Connection.Connect(fmt.Sprintf("%s:%d", c.Server, c.Port))
	if err != nil {
		return fmt.Errorf("nie udało się połączyć z %s:%d - %v", c.Server, c.Port, err)
	}

	c.IsConnected = true

	// Callback po pomyślnym połączeniu
	c.Connection.AddCallback("001", func(e *irc.Event) {
		util.Info("Połączono z %s jako %s", c.Server, c.Nick)
	})

	// Obsługa PING/PONG
	c.Connection.AddCallback("PING", func(e *irc.Event) {
		c.Connection.SendRawf("PONG :%s", e.Message())
	})

	// Uruchomienie pętli obsługi zdarzeń
	go c.Connection.Loop()
	return nil
}

// Disconnect rozłącza klienta z serwerem IRC.
func (c *Client) Disconnect() {
	if c.IsConnected {
		c.Connection.Quit()
		c.IsConnected = false
	}
}

// Join dołącza do podanego kanału.
func (c *Client) Join(channel string) {
	if c.IsConnected {
		c.Connection.Join(channel)
	}
}

// Part opuszcza podany kanał.
func (c *Client) Part(channel string) {
	if c.IsConnected {
		c.Connection.Part(channel)
	}
}

// SendMessage wysyła wiadomość na wskazany target (kanał lub użytkownik).
func (c *Client) SendMessage(target, message string) {
	if c.IsConnected {
		c.Connection.Privmsg(target, message)
	}
}

// SendNotice wysyła notice na wskazany target.
func (c *Client) SendNotice(target, message string) {
	if c.IsConnected {
		c.Connection.Notice(target, message)
	}
}

// ChangeNick zmienia nick klienta.
func (c *Client) ChangeNick(newNick string) {
	if c.IsConnected {
		c.Connection.Nick(newNick)
		c.Nick = newNick
	}
}

// AddCallback dodaje callback dla podanego zdarzenia IRC.
func (c *Client) AddCallback(event string, callback func(*irc.Event)) {
	c.Connection.AddCallback(event, callback)
}

// SendRaw wysyła surową wiadomość IRC.
func (c *Client) SendRaw(message string) {
	if c.IsConnected {
		c.Connection.SendRaw(message)
	}
}

// SendISON wysyła polecenie ISON z listą pseudonimów.
func (c *Client) SendISON(nicks []string) {
	if c.IsConnected {
		c.Connection.SendRawf("ISON %s", strings.Join(nicks, " "))
	}
}
