// Plik internal/util/nickgen.go

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

func GenerateRandomNick(apiURL string, maxWordLength int, timeoutSeconds int) (string, error) {
	client := http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}

	// Dodajemy parametry do URL
	fullURL := fmt.Sprintf("%s?upto=%d&count=100", apiURL, maxWordLength)

	resp, err := client.Get(fullURL)
	if err != nil {
		return "", fmt.Errorf("błąd podczas pobierania nicków z API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API zwróciło status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("błąd podczas odczytu odpowiedzi API: %v", err)
	}

	var apiResp APIResponse
	err = json.Unmarshal(body, &apiResp)
	if err != nil {
		return "", fmt.Errorf("błąd podczas parsowania odpowiedzi API: %v", err)
	}

	if len(apiResp.Words) == 0 {
		return "", fmt.Errorf("API nie zwróciło żadnych słów")
	}

	// Filtrujemy słowa o odpowiedniej długości i składające się tylko z liter
	validWords := []string{}
	for _, word := range apiResp.Words {
		word = strings.TrimSpace(word)
		if len(word) >= 3 && len(word) <= maxWordLength && isAlpha(word) {
			validWords = append(validWords, capitalize(word))
		}
	}

	if len(validWords) == 0 {
		return "", fmt.Errorf("brak odpowiednich słów po filtracji")
	}

	// Losowo wybieramy jedno słowo
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
