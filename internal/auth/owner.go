package auth

import (
	"encoding/json"
	"os"

	"github.com/kofany/gNb/internal/util" // Import modułu util z matcherem
	irc "github.com/kofany/go-ircevent"
)

// OwnerList zawiera listę właścicieli zdefiniowanych w pliku JSON.
type OwnerList struct {
	Owners []string `json:"owners"`
}

// LoadOwners wczytuje listę właścicieli z pliku JSON.
func LoadOwners(filename string) (OwnerList, error) {
	var owners OwnerList
	data, err := os.ReadFile(filename)
	if err != nil {
		return owners, err
	}
	err = json.Unmarshal(data, &owners)
	return owners, err
}

// IsOwner sprawdza, czy użytkownik jest właścicielem.
func IsOwner(event *irc.Event, owners OwnerList) bool {
	// Tworzymy instancję matchera
	matcher := util.Matcher{}
	userHost := event.Nick + "!" + event.User + "@" + event.Host

	// Sprawdzamy, czy userHost pasuje do którejś z masek właścicieli
	for _, ownerMask := range owners.Owners {
		if matcher.MatchUserHost(ownerMask, userHost) {
			return true
		}
	}
	return false
}
