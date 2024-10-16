package util

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

type APIResponse struct {
	Words []string `json:"words"`
}

func GetWordsFromAPI(apiURL string, maxWordLength, timeout, count int) ([]string, error) {
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	url := fmt.Sprintf("%s?count=%d&length=%d", apiURL, count, maxWordLength)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error fetching words from API: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading API response: %v", err)
	}

	var response struct {
		Words []string `json:"words"`
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, fmt.Errorf("error parsing API response: %v", err)
	}

	if len(response.Words) < count {
		return nil, fmt.Errorf("API returned fewer words than requested")
	}

	return response.Words[:count], nil
}

func GenerateRandomNick(apiURL string, maxWordLength int, timeoutSeconds int) (string, error) {
	client := http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}

	// Add parameters to URL
	fullURL := fmt.Sprintf("%s?upto=%d&count=100", apiURL, maxWordLength)

	resp, err := client.Get(fullURL)
	if err != nil {
		return "", fmt.Errorf("error fetching nicks from API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading API response: %v", err)
	}

	var apiResp APIResponse
	err = json.Unmarshal(body, &apiResp)
	if err != nil {
		return "", fmt.Errorf("error parsing API response: %v", err)
	}

	if len(apiResp.Words) == 0 {
		return "", fmt.Errorf("API returned no words")
	}

	// Filter words of appropriate length and letters only
	validWords := []string{}
	for _, word := range apiResp.Words {
		word = strings.TrimSpace(word)
		if len(word) >= 3 && len(word) <= maxWordLength && isAlpha(word) {
			validWords = append(validWords, capitalize(word))
		}
	}

	if len(validWords) == 0 {
		return "", fmt.Errorf("no suitable words after filtering")
	}

	// Randomly select one word
	nick := validWords[rand.Intn(len(validWords))]
	return nick, nil
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

func GenerateFallbackNick() string {
	return fmt.Sprintf("Bot%d", time.Now().UnixNano()%10000)
}
