package util

import (
	"crypto/tls"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type APIResponse struct {
	Words []string `json:"words"`
}

type LocalWordSource struct {
	words []string
	mu    sync.RWMutex
}

var (
	localSource *LocalWordSource
	sourceMu    sync.RWMutex
)

func init() {
	// Inicjalizacja lokalnego źródła przy starcie
	source, err := LoadWordsFromGob(filepath.Join("data", "words.gob"))
	if err == nil {
		sourceMu.Lock()
		localSource = source
		sourceMu.Unlock()
	}
}

func LoadWordsFromGob(filepath string) (*LocalWordSource, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("could not open words file: %v", err)
	}
	defer file.Close()

	var words []string
	decoder := gob.NewDecoder(file)
	if err := decoder.Decode(&words); err != nil {
		return nil, fmt.Errorf("error decoding words: %v", err)
	}

	return &LocalWordSource{
		words: words,
	}, nil
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

func GetWordsFromAPI(apiURL string, maxWordLength, timeout, count int) ([]string, error) {
	sourceMu.RLock()
	if localSource != nil {
		words, err := localSource.GetRandomWords(count)
		sourceMu.RUnlock()
		if err == nil {
			return words, nil
		}
	} else {
		sourceMu.RUnlock()
	}

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	url := fmt.Sprintf("%s?count=%d&length=%d", apiURL, count, maxWordLength)
	resp, err := client.Get(url)
	if err != nil {
		return generateFallbackWords(count), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return generateFallbackWords(count), nil
	}

	var response APIResponse
	err = json.Unmarshal(body, &response)
	if err != nil || len(response.Words) < count {
		return generateFallbackWords(count), nil
	}

	return response.Words[:count], nil
}

func GenerateRandomNick(apiURL string, maxWordLength int, timeoutSeconds int) (string, error) {
	sourceMu.RLock()
	if localSource != nil {
		words, err := localSource.GetRandomWords(1)
		sourceMu.RUnlock()
		if err == nil && len(words) > 0 {
			return capitalize(words[0]), nil
		}
	} else {
		sourceMu.RUnlock()
	}

	client := &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	fullURL := fmt.Sprintf("%s?upto=%d&count=100", apiURL, maxWordLength)
	resp, err := client.Get(fullURL)
	if err != nil {
		return GenerateFallbackNick(), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return GenerateFallbackNick(), nil
	}

	var apiResp APIResponse
	err = json.Unmarshal(body, &apiResp)
	if err != nil || len(apiResp.Words) == 0 {
		return GenerateFallbackNick(), nil
	}

	validWords := []string{}
	for _, word := range apiResp.Words {
		word = strings.TrimSpace(word)
		if len(word) >= 3 && len(word) <= maxWordLength && isAlpha(word) {
			validWords = append(validWords, capitalize(word))
		}
	}

	if len(validWords) == 0 {
		return GenerateFallbackNick(), nil
	}

	return validWords[rand.Intn(len(validWords))], nil
}

func generateFallbackWords(count int) []string {
	words := make([]string, count)
	for i := 0; i < count; i++ {
		words[i] = GenerateFallbackNick()
	}
	return words
}

func GenerateFallbackNick() string {
	// Próba pobrania słowa z lokalnego źródła
	sourceMu.RLock()
	if localSource != nil {
		words, err := localSource.GetRandomWords(1)
		sourceMu.RUnlock()
		if err == nil && len(words) > 0 {
			return capitalize(words[0])
		}
	} else {
		sourceMu.RUnlock()
	}

	// Jeśli nie udało się pobrać z lokalnego źródła, generujemy losowy nick
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	nick := make([]byte, 8)
	for i := range nick {
		nick[i] = charset[rand.Intn(len(charset))]
	}
	return string(nick)
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(string(s[0])) + strings.ToLower(s[1:])
}
