package util

import (
	"encoding/gob"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type LocalWordSource struct {
	words []string
	mu    sync.RWMutex
}

var (
	localSource     *LocalWordSource
	sourceMu        sync.RWMutex
	leadWords       []string
	tailWords       []string
	nickPartsMu     sync.RWMutex
	recentNickMu    sync.Mutex
	recentNickSet   = make(map[string]struct{})
	recentNickQueue []string
)

const (
	defaultNickLength = 15
	maxNickLengthCap  = 15
	recentNickWindow  = 512
)

var fallbackLeads = []string{
	"silent", "rough", "fuzzy", "brave", "quick", "gritty", "nippy", "plain",
	"wild", "fancy", "slick", "dusty", "shady", "stormy", "steady", "witty",
	"smoky", "mellow", "nimble", "daring", "snug", "peachy", "weird", "neat",
}

var fallbackTails = []string{
	"bull", "panda", "rider", "scout", "druid", "crab", "goat", "blaze",
	"stalk", "drag", "manta", "troll", "beast", "owl", "raven", "wolf",
	"otter", "badger", "falcon", "viper", "warden", "runner", "drake", "fox",
}

func init() {
	if source, err := LoadWordsFromGob(filepath.Join("data", "words.gob")); err == nil {
		sourceMu.Lock()
		localSource = source
		sourceMu.Unlock()
	}

	if words, err := loadStringSliceGob(filepath.Join("data", "nick_leads.gob")); err == nil && len(words) > 0 {
		nickPartsMu.Lock()
		leadWords = words
		nickPartsMu.Unlock()
	}
	if words, err := loadStringSliceGob(filepath.Join("data", "nick_tails.gob")); err == nil && len(words) > 0 {
		nickPartsMu.Lock()
		tailWords = words
		nickPartsMu.Unlock()
	}
}

func LoadWordsFromGob(filepath string) (*LocalWordSource, error) {
	words, err := loadStringSliceGob(filepath)
	if err != nil {
		return nil, fmt.Errorf("could not open words file: %v", err)
	}

	return &LocalWordSource{
		words: words,
	}, nil
}

func loadStringSliceGob(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var words []string
	if err := gob.NewDecoder(file).Decode(&words); err != nil {
		return nil, fmt.Errorf("error decoding words: %v", err)
	}
	return words, nil
}

func (lws *LocalWordSource) GetRandomWords(count int) ([]string, error) {
	lws.mu.RLock()
	defer lws.mu.RUnlock()

	if len(lws.words) < count {
		return nil, fmt.Errorf("not enough words available")
	}

	indices := make([]int, len(lws.words))
	for i := range indices {
		indices[i] = i
	}
	rand.Shuffle(len(indices), func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = lws.words[indices[i]]
	}
	return result, nil
}

func GetWordsFromAPI(_ string, maxWordLength, _ int, count int) ([]string, error) {
	return generateNickPool(count, normalizedNickLength(maxWordLength))
}

func GenerateRandomNick(_ string, maxWordLength int, _ int) (string, error) {
	return generateComposedNick(normalizedNickLength(maxWordLength))
}

func generateNickPool(count, maxWordLength int) ([]string, error) {
	seen := make(map[string]struct{}, count)
	nicks := make([]string, 0, count)

	maxAttempts := count * 64
	for attempts := 0; attempts < maxAttempts && len(nicks) < count; attempts++ {
		nick, err := generateComposedNick(maxWordLength)
		if err != nil {
			continue
		}
		if _, exists := seen[nick]; exists {
			continue
		}
		seen[nick] = struct{}{}
		nicks = append(nicks, nick)
	}

	if len(nicks) < count {
		return nil, fmt.Errorf("generated only %d unique nicks out of %d requested", len(nicks), count)
	}
	return nicks, nil
}

func generateComposedNick(maxWordLength int) (string, error) {
	leads, tails := getNickParts()
	if len(leads) == 0 || len(tails) == 0 {
		return "", fmt.Errorf("nick banks are empty")
	}

	for attempts := 0; attempts < 256; attempts++ {
		lead := leads[rand.Intn(len(leads))]
		tail := tails[rand.Intn(len(tails))]

		nick, ok := combineNickParts(lead, tail, maxWordLength)
		if !ok || wasRecentlyGenerated(nick) {
			continue
		}

		markRecentlyGenerated(nick)
		return nick, nil
	}

	return "", fmt.Errorf("failed to generate a valid nick")
}

func getNickParts() ([]string, []string) {
	nickPartsMu.RLock()
	defer nickPartsMu.RUnlock()

	leads := leadWords
	if len(leads) == 0 {
		leads = fallbackLeads
	}
	tails := tailWords
	if len(tails) == 0 {
		tails = fallbackTails
	}
	return leads, tails
}

func combineNickParts(lead, tail string, maxWordLength int) (string, bool) {
	maxWordLength = normalizedNickLength(maxWordLength)

	if len(lead)+len(tail) > maxWordLength {
		return "", false
	}

	nick := lead + tail
	if !isNickCandidate(nick, maxWordLength) {
		return "", false
	}
	if strings.HasPrefix(tail, lead) || strings.HasSuffix(lead, tail) {
		return "", false
	}

	return nick, true
}

func isNickCandidate(nick string, maxLen int) bool {
	if len(nick) < 6 || len(nick) > maxLen || !isAlpha(nick) {
		return false
	}

	vowels := 0
	repeat := 1
	for i, r := range nick {
		switch r {
		case 'a', 'e', 'i', 'o', 'u', 'y':
			vowels++
		}

		if i > 0 && nick[i] == nick[i-1] {
			repeat++
			if repeat >= 3 {
				return false
			}
		} else {
			repeat = 1
		}
	}

	if vowels < 2 {
		return false
	}

	for _, ugly := range []string{
		"qj", "jq", "zx", "xz", "vv", "ww", "jj", "kkk", "qq", "yyy",
	} {
		if strings.Contains(nick, ugly) {
			return false
		}
	}

	return true
}

func wasRecentlyGenerated(nick string) bool {
	recentNickMu.Lock()
	defer recentNickMu.Unlock()
	_, exists := recentNickSet[nick]
	return exists
}

func markRecentlyGenerated(nick string) {
	recentNickMu.Lock()
	defer recentNickMu.Unlock()

	if _, exists := recentNickSet[nick]; exists {
		return
	}

	recentNickSet[nick] = struct{}{}
	recentNickQueue = append(recentNickQueue, nick)

	if len(recentNickQueue) > recentNickWindow {
		oldest := recentNickQueue[0]
		recentNickQueue = recentNickQueue[1:]
		delete(recentNickSet, oldest)
	}
}

func generateFallbackWords(count int) []string {
	words := make([]string, count)
	for i := 0; i < count; i++ {
		words[i] = GenerateFallbackNick()
	}
	return words
}

func GenerateFallbackNick() string {
	nick, err := generateComposedNick(defaultNickLength)
	if err == nil {
		return nick
	}

	const charset = "abcdefghijklmnopqrstuvwxyz"
	value := make([]byte, 8)
	for i := range value {
		value[i] = charset[rand.Intn(len(charset))]
	}
	return string(value)
}

func normalizedNickLength(length int) int {
	if length <= 0 {
		return defaultNickLength
	}
	if length > maxNickLengthCap {
		return maxNickLengthCap
	}
	return length
}

func isAlpha(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
