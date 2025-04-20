package nickcatcher

import (
	"sync"
)

var (
	globalInstance *NickCatcher
	globalMutex    sync.RWMutex
)

// SetGlobalInstance ustawia globalną instancję NickCatcher
func SetGlobalInstance(nc *NickCatcher) {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	globalInstance = nc
}

// GetGlobalInstance zwraca globalną instancję NickCatcher
func GetGlobalInstance() *NickCatcher {
	globalMutex.RLock()
	defer globalMutex.RUnlock()
	return globalInstance
}

// IsPriorityNick sprawdza, czy nick jest na liście priorytetowych
// Funkcja eksportowana dla innych pakietów
func IsPriorityNick(nick string) bool {
	nc := GetGlobalInstance()
	if nc == nil {
		return false
	}
	return nc.isPriorityNick(nick)
}
