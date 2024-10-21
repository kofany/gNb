package dcc

import (
	"bufio"
	"net"
	"strings"
	"sync"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type DCCTunnel struct {
	conn          net.Conn
	bot           types.Bot
	active        bool
	mu            sync.Mutex
	ignoredEvents map[string]bool
	onStop        func()
}

func NewDCCTunnel(bot types.Bot, onStop func()) *DCCTunnel {
	return &DCCTunnel{
		bot:           bot,
		active:        false,
		ignoredEvents: map[string]bool{"303": true}, // Ignore ISON responses
		onStop:        onStop,
	}
}

func (dt *DCCTunnel) Start(conn net.Conn) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if dt.active {
		util.Warning("DCC tunnel already active for bot %s", dt.bot.GetCurrentNick())
		return
	}

	dt.conn = conn
	dt.active = true

	welcomeMessage := `
Welcome to the DCC Chat connection!

Type your IRC commands here.

`
	dt.conn.Write([]byte(welcomeMessage))

	go dt.readFromConn()
}

func (dt *DCCTunnel) Stop() {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if !dt.active {
		return
	}

	dt.active = false
	if dt.conn != nil {
		dt.conn.Close()
	}

	// Call the onStop callback
	if dt.onStop != nil {
		dt.onStop()
	}
}

func (dt *DCCTunnel) readFromConn() {
	defer dt.Stop()

	scanner := bufio.NewScanner(dt.conn)
	for scanner.Scan() {
		command := scanner.Text()

		dt.bot.SendRaw(command)
	}

	if err := scanner.Err(); err != nil {
		util.Error("Error reading from DCC Chat connection: %v", err)
	}
}

func (dt *DCCTunnel) WriteToConn(data string) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if !dt.active || dt.conn == nil {
		return
	}

	// Ignore certain events
	if strings.Contains(data, " 303 ") {
		return
	}

	dt.conn.Write([]byte(data + "\r\n"))
}
