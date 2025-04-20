package dcc

import (
	"fmt"

	"github.com/kofany/gNb/internal/nickcatcher"
	"github.com/kofany/gNb/internal/util"
)

// getNickCatcher próbuje uzyskać dostęp do systemu przechwytywania nicków
func (dt *DCCTunnel) getNickCatcher() (*nickcatcher.NickCatcher, error) {
	// Pobierz globalną instancję NickCatcher
	nc := nickcatcher.GetNickCatcherInstance()
	if nc == nil {
		return nil, fmt.Errorf("nick catcher system not available")
	}
	return nc, nil
}

// handleNickListCommand obsługuje komendę wyświetlającą listę priorytetowych nicków
func (dt *DCCTunnel) handleNickListCommand(args []string) {
	nc, err := dt.getNickCatcher()
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	response, err := nc.ExecuteNickCatcherCommand("nicklist", args)
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	dt.sendToClient(response)
}

// handleAddNickCommand obsługuje komendę dodającą nick do listy priorytetowych
func (dt *DCCTunnel) handleAddNickCommand(args []string) {
	if len(args) < 1 {
		dt.sendToClient("Usage: .addnick <nick>")
		return
	}

	nc, err := dt.getNickCatcher()
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	response, err := nc.ExecuteNickCatcherCommand("addnick", args)
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	dt.sendToClient(response)
	util.Info("Nick %s added to priority list by %s", args[0], dt.ownerNick)
}

// handleDelNickCommand obsługuje komendę usuwającą nick z listy priorytetowych
func (dt *DCCTunnel) handleDelNickCommand(args []string) {
	if len(args) < 1 {
		dt.sendToClient("Usage: .delnick <nick>")
		return
	}

	nc, err := dt.getNickCatcher()
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	response, err := nc.ExecuteNickCatcherCommand("delnick", args)
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	dt.sendToClient(response)
	util.Info("Nick %s removed from priority list by %s", args[0], dt.ownerNick)
}

// handleNickStatusCommand obsługuje komendę wyświetlającą status systemu przechwytywania nicków
func (dt *DCCTunnel) handleNickStatusCommand(args []string) {
	nc, err := dt.getNickCatcher()
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	response, err := nc.ExecuteNickCatcherCommand("nickstatus", args)
	if err != nil {
		dt.sendToClient(fmt.Sprintf("Error: %v", err))
		return
	}

	dt.sendToClient(response)
}
