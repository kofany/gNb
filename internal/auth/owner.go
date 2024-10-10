package auth

import (
	"encoding/json"
	"os"

	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevent"
)

// OwnerList contains the list of owners defined in the JSON file.
type OwnerList struct {
	Owners []string `json:"owners"`
}

// LoadOwners loads the list of owners from a JSON file.
func LoadOwners(filename string) (OwnerList, error) {
	var owners OwnerList
	data, err := os.ReadFile(filename)
	if err != nil {
		return owners, err
	}
	err = json.Unmarshal(data, &owners)
	return owners, err
}

// IsOwner checks if a user is an owner.
func IsOwner(event *irc.Event, owners OwnerList) bool {
	// Create an instance of Matcher
	matcher := util.Matcher{}
	userHost := event.Nick + "!" + event.User + "@" + event.Host

	// Check if userHost matches any owner masks
	for _, ownerMask := range owners.Owners {
		if matcher.MatchUserHost(ownerMask, userHost) {
			return true
		}
	}
	return false
}
