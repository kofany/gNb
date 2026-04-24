package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeRequestOK(t *testing.T) {
	raw := []byte(`{"type":"request","id":"x","method":"foo","params":{"a":1}}`)
	r, err := DecodeRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "foo" || r.ID != "x" {
		t.Fatalf("bad parse: %+v", r)
	}
}

func TestDecodeRequestRejectsWrongType(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"type":"event","id":"x","method":"y"}`))
	if err == nil || !strings.Contains(err.Error(), "request") {
		t.Fatalf("want type error, got %v", err)
	}
}

func TestDecodeRequestMissingFields(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"type":"request","id":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "method") {
		t.Fatalf("want missing field error, got %v", err)
	}
}

func TestResponseJSONRoundTrip(t *testing.T) {
	m := NewResponse("7", map[string]string{"hello": "world"})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"type":"response"`) {
		t.Fatalf("missing type field: %s", b)
	}
	if !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("missing ok field: %s", b)
	}
}

func TestEventJSONShape(t *testing.T) {
	m := NewEvent("node1", "bot.connected", 42, "2026-01-01T00:00:00Z", map[string]string{"bot_id": "abc"})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"type":"event"`, `"event":"bot.connected"`, `"node_id":"node1"`, `"seq":42`} {
		if !strings.Contains(s, want) {
			t.Errorf("event missing %s in %s", want, s)
		}
	}
}
