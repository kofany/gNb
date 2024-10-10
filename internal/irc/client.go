package irc

import (
	"fmt"
	"strings"

	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevent"
)

// Client is an abstraction of an IRC connection
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

// NewClient creates a new IRC client instance
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

// Connect establishes a connection to the IRC server
func (c *Client) Connect() error {
	c.Connection = irc.IRC(c.Nick, c.Username, c.Vhost)
	c.Connection.VerboseCallbackHandler = false
	c.Connection.Debug = false
	c.Connection.UseTLS = c.SSL
	c.Connection.RealName = c.Realname

	err := c.Connection.Connect(fmt.Sprintf("%s:%d", c.Server, c.Port))
	if err != nil {
		return fmt.Errorf("failed to connect to %s:%d - %v", c.Server, c.Port, err)
	}

	c.IsConnected = true

	// Callback upon successful connection
	c.Connection.AddCallback("001", func(e *irc.Event) {
		util.Info("Connected to %s as %s", c.Server, c.Nick)
	})

	// Handle PING/PONG
	c.Connection.AddCallback("PING", func(e *irc.Event) {
		c.Connection.SendRawf("PONG :%s", e.Message())
	})

	// Start the event handling loop
	go c.Connection.Loop()
	return nil
}

// Disconnect disconnects the client from the IRC server
func (c *Client) Disconnect() {
	if c.IsConnected {
		c.Connection.Quit()
		c.IsConnected = false
	}
}

// Join joins the specified channel
func (c *Client) Join(channel string) {
	if c.IsConnected {
		c.Connection.Join(channel)
	}
}

// Part leaves the specified channel
func (c *Client) Part(channel string) {
	if c.IsConnected {
		c.Connection.Part(channel)
	}
}

// SendMessage sends a message to the specified target
func (c *Client) SendMessage(target, message string) {
	if c.IsConnected {
		c.Connection.Privmsg(target, message)
	}
}

// SendNotice sends a notice to the specified target
func (c *Client) SendNotice(target, message string) {
	if c.IsConnected {
		c.Connection.Notice(target, message)
	}
}

// ChangeNick changes the client's nick
func (c *Client) ChangeNick(newNick string) {
	if c.IsConnected {
		c.Connection.Nick(newNick)
		c.Nick = newNick
	}
}

// AddCallback adds a callback for the specified IRC event
func (c *Client) AddCallback(event string, callback func(*irc.Event)) {
	c.Connection.AddCallback(event, callback)
}

// SendRaw sends a raw IRC message
func (c *Client) SendRaw(message string) {
	if c.IsConnected {
		c.Connection.SendRaw(message)
	}
}

// SendISON sends an ISON command with a list of nicknames
func (c *Client) SendISON(nicks []string) {
	if c.IsConnected {
		c.Connection.SendRawf("ISON %s", strings.Join(nicks, " "))
	}
}
