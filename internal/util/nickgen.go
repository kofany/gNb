package util

import (
	"crypto/tls"
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
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
	client := &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
