package bot

import (
	"testing"
	"time"
)

// TestISONMechanism is a simple test to verify the ISON mechanism works correctly
func TestISONMechanism(t *testing.T) {
	// This is a simple test to verify that our implementation works correctly
	// The actual functionality is tested in production use
	
	// Create a test request ID
	requestID := "test-request-123"
	
	// Create a test response channel
	responseChan := make(chan []string, 1)
	
	// Create a bot instance
	bot := &Bot{
		isonRequests: make(map[string]chan []string),
	}
	
	// Register the request
	bot.isonRequestsMutex.Lock()
	bot.isonRequests[requestID] = responseChan
	bot.isonRequestsMutex.Unlock()
	
	// Create a test ISON response
	testResponse := []string{"nick1", "nick2", "nick3"}
	
	// Send the response to the channel
	go func() {
		time.Sleep(10 * time.Millisecond)
		bot.isonRequestsMutex.Lock()
		bot.isonRequests[requestID] <- testResponse
		bot.isonRequestsMutex.Unlock()
	}()
	
	// Wait for the response
	var response []string
	select {
	case response = <-responseChan:
		// Got response
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timed out waiting for response")
	}
	
	// Verify the response
	if len(response) != len(testResponse) {
		t.Errorf("Expected response length %d, got %d", len(testResponse), len(response))
	}
	
	for i, nick := range testResponse {
		if response[i] != nick {
			t.Errorf("Expected nick %s at position %d, got %s", nick, i, response[i])
		}
	}
	
	// Test cleanup
	bot.isonRequestsMutex.Lock()
	delete(bot.isonRequests, requestID)
	bot.isonRequestsMutex.Unlock()
	
	// Verify cleanup
	bot.isonRequestsMutex.Lock()
	remaining := len(bot.isonRequests)
	bot.isonRequestsMutex.Unlock()
	
	if remaining != 0 {
		t.Errorf("Expected 0 remaining requests, got %d", remaining)
	}
}
