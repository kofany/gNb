package api

import (
	"encoding/json"
	"fmt"
)

type ErrorCode string

const (
	ErrUnauthorized  ErrorCode = "unauthorized"
	ErrUnknownMethod ErrorCode = "unknown_method"
	ErrInvalidParams ErrorCode = "invalid_params"
	ErrNotFound      ErrorCode = "not_found"
	ErrForbidden     ErrorCode = "forbidden"
	ErrRateLimited   ErrorCode = "rate_limited"
	ErrCooldown      ErrorCode = "cooldown"
	ErrInternal      ErrorCode = "internal"
)

type RequestMsg struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ResponseMsg struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result"`
}

type ErrorMsg struct {
	Type    string    `json:"type"`
	ID      string    `json:"id,omitempty"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

type EventMsg struct {
	Type   string `json:"type"`
	Event  string `json:"event"`
	NodeID string `json:"node_id"`
	TS     string `json:"ts"`
	Seq    uint64 `json:"seq"`
	Data   any    `json:"data"`
}

func NewResponse(id string, result any) ResponseMsg {
	return ResponseMsg{Type: "response", ID: id, OK: true, Result: result}
}

func NewError(id string, code ErrorCode, msg string) ErrorMsg {
	return ErrorMsg{Type: "error", ID: id, Code: code, Message: msg}
}

func NewEvent(nodeID, event string, seq uint64, ts string, data any) EventMsg {
	return EventMsg{Type: "event", Event: event, NodeID: nodeID, Seq: seq, TS: ts, Data: data}
}

// DecodeRequest returns the parsed request and an error if the message is not
// a valid inbound envelope.
func DecodeRequest(raw []byte) (*RequestMsg, error) {
	var r RequestMsg
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if r.Type != "request" {
		return nil, fmt.Errorf("expected type=request, got %q", r.Type)
	}
	if r.ID == "" || r.Method == "" {
		return nil, fmt.Errorf("request missing id or method")
	}
	return &r, nil
}
