# Panel API (WebSocket) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a WebSocket-based control plane (`internal/api`) to gNb that lets a web panel drive a node live — full DCC-equivalent actions per bot and node, plus lifecycle events and an attach pipe for per-bot IRC firehose.

**Architecture:** New `internal/api` package runs an HTTP server (plain HTTP by default for cloudflared; optional native TLS). Authenticated WS sessions exchange a JSON envelope (`request`/`response`/`event`/`error`). An `EventHub` with ring buffer publishes lifecycle events; an `AttachManager` fans out per-bot IRC events to opted-in sessions. Bot/BotManager/NickManager emit through a new `types.EventSink` interface that the API implements — no direct coupling.

**Tech Stack:** Go 1.23, `github.com/coder/websocket v1.8.14`, stdlib `net/http`, `gopkg.in/yaml.v2`, existing `go-ircevo v1.2.4`.

**Spec:** `docs/superpowers/specs/2026-04-24-api-websocket-design.md`.

---

## Ground rules for every task

- TDD: write failing test, run to confirm red, implement, run to confirm green, commit.
- One commit per task with message starting `api:` (scope).
- `gofmt -w` on every file you touch before commit.
- After Tasks touching non-api files, run `go vet ./...` before commit.
- After Tasks with concurrency (sessions, hub), run `go test -race ./internal/api/...`.
- Working dir: `/home/projekt/dev/gnb`.

---

## Task 1: Add `github.com/coder/websocket` dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1:** `cd /home/projekt/dev/gnb && go get github.com/coder/websocket@v1.8.14`
- [ ] **Step 2:** `go mod tidy`
- [ ] **Step 3:** Verify: `grep 'coder/websocket' go.mod` should show `v1.8.14`.
- [ ] **Step 4:** `go build ./...` — must succeed (nothing uses the dep yet; this only confirms it resolves).
- [ ] **Step 5:** Commit: `git add go.mod go.sum && git commit -m "api: add github.com/coder/websocket v1.8.14 dependency"`

---

## Task 2: `APIConfig` struct + YAML parsing + validation

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1:** Add to `internal/config/config.go` above `func LoadConfig`:

```go
type APIConfig struct {
	Enabled        bool   `yaml:"enabled"`
	NodeName       string `yaml:"node_name"`
	BindAddr       string `yaml:"bind_addr"`
	AuthToken      string `yaml:"auth_token"`
	TLSCertFile    string `yaml:"tls_cert_file"`
	TLSKeyFile     string `yaml:"tls_key_file"`
	EventBuffer    int    `yaml:"event_buffer"`
	MaxConnections int    `yaml:"max_connections"`
}

func (a *APIConfig) ApplyDefaults() {
	if a.BindAddr == "" {
		a.BindAddr = "127.0.0.1:7766"
	}
	if a.EventBuffer <= 0 {
		a.EventBuffer = 1000
	}
	if a.MaxConnections <= 0 {
		a.MaxConnections = 4
	}
	if a.NodeName == "" {
		a.NodeName = "gnb-node"
	}
}

func (a *APIConfig) Validate() error {
	if !a.Enabled {
		return nil
	}
	if len(a.AuthToken) < 32 {
		return fmt.Errorf("api.auth_token must be at least 32 characters when api.enabled: true")
	}
	if (a.TLSCertFile == "") != (a.TLSKeyFile == "") {
		return fmt.Errorf("api.tls_cert_file and api.tls_key_file must be set together or both empty")
	}
	if _, _, err := net.SplitHostPort(a.BindAddr); err != nil {
		return fmt.Errorf("api.bind_addr invalid: %w", err)
	}
	return nil
}
```

Add `"net"` to imports.

- [ ] **Step 2:** Add field to `Config`:

```go
type Config struct {
	Global   GlobalConfig `yaml:"global"`
	Bots     []BotConfig  `yaml:"bots"`
	Channels []string     `yaml:"channels"`
	API      APIConfig    `yaml:"api"`
}
```

- [ ] **Step 3:** In `LoadConfig`, after successful unmarshal, call `config.API.ApplyDefaults()` then `if err := config.API.Validate(); err != nil { return nil, err }`.

- [ ] **Step 4:** Create `internal/config/config_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

func TestAPIConfigDefaults(t *testing.T) {
	a := APIConfig{}
	a.ApplyDefaults()
	if a.BindAddr != "127.0.0.1:7766" {
		t.Errorf("BindAddr default: got %q", a.BindAddr)
	}
	if a.EventBuffer != 1000 {
		t.Errorf("EventBuffer default: got %d", a.EventBuffer)
	}
	if a.MaxConnections != 4 {
		t.Errorf("MaxConnections default: got %d", a.MaxConnections)
	}
	if a.NodeName != "gnb-node" {
		t.Errorf("NodeName default: got %q", a.NodeName)
	}
}

func TestAPIConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     APIConfig
		wantErr string
	}{
		{"disabled passes", APIConfig{Enabled: false}, ""},
		{"short token", APIConfig{Enabled: true, AuthToken: "abc", BindAddr: "127.0.0.1:1"}, "auth_token"},
		{"cert without key", APIConfig{Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "127.0.0.1:1", TLSCertFile: "c.pem"}, "tls_cert_file"},
		{"bad bind", APIConfig{Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "no-colon"}, "bind_addr"},
		{"ok plain", APIConfig{Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "127.0.0.1:7766"}, ""},
	}
	for _, c := range cases {
		err := c.cfg.Validate()
		if c.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.wantErr, err)
		}
	}
}
```

- [ ] **Step 5:** Run: `go test ./internal/config/... -run APIConfig -v` — must pass.
- [ ] **Step 6:** `gofmt -w internal/config/ && go vet ./internal/config/...`
- [ ] **Step 7:** Commit: `git add internal/config && git commit -m "api: add APIConfig with defaults and validation"`

---

## Task 3: `bot_id` computation

**Files:**
- Create: `internal/api/bot_id.go`
- Create: `internal/api/bot_id_test.go`

- [ ] **Step 1:** Create `internal/api/bot_id.go`:

```go
package api

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// ComputeBotID derives a stable 12-char hex identifier for a bot from its
// IRC connection tuple and its index within cfg.Bots. The index disambiguates
// multiple bots that share {server, port, vhost}.
func ComputeBotID(server string, port int, vhost string, index int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%s:%d", server, port, vhost, index)))
	return hex.EncodeToString(sum[:])[:12]
}
```

- [ ] **Step 2:** Create `internal/api/bot_id_test.go`:

```go
package api

import "testing"

func TestComputeBotIDDeterministic(t *testing.T) {
	a := ComputeBotID("irc.example", 6667, "1.2.3.4", 0)
	b := ComputeBotID("irc.example", 6667, "1.2.3.4", 0)
	if a != b {
		t.Fatalf("same input should produce same id: %s vs %s", a, b)
	}
	if len(a) != 12 {
		t.Fatalf("id must be 12 chars, got %d", len(a))
	}
}

func TestComputeBotIDDistinguishesDuplicates(t *testing.T) {
	a := ComputeBotID("irc.example", 6667, "1.2.3.4", 0)
	b := ComputeBotID("irc.example", 6667, "1.2.3.4", 1)
	if a == b {
		t.Fatalf("index must disambiguate duplicates: %s == %s", a, b)
	}
}

func TestComputeBotIDDistinguishesTuple(t *testing.T) {
	a := ComputeBotID("irc.a", 6667, "vh", 0)
	b := ComputeBotID("irc.b", 6667, "vh", 0)
	if a == b {
		t.Fatalf("different server must produce different id")
	}
}
```

- [ ] **Step 3:** `mkdir -p internal/api && go test ./internal/api/... -v`
- [ ] **Step 4:** `gofmt -w internal/api/`
- [ ] **Step 5:** Commit: `git add internal/api/bot_id.go internal/api/bot_id_test.go && git commit -m "api: add ComputeBotID with index-disambiguated hash"`

---

## Task 4: `node_id` persistence

**Files:**
- Create: `internal/api/node_id.go`
- Create: `internal/api/node_id_test.go`

- [ ] **Step 1:** Create `internal/api/node_id.go`:

```go
package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const nodeIDFile = "data/node_id"

var nodeIDMu sync.Mutex

// LoadOrCreateNodeID returns the persistent node UUID, creating the file on first call.
// path is the file location (tests pass a temp dir; production uses data/node_id).
func LoadOrCreateNodeID(path string) (string, error) {
	nodeIDMu.Lock()
	defer nodeIDMu.Unlock()

	if raw, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(raw))
		if len(id) == 32 {
			return id, nil
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate node id: %w", err)
	}
	id := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir for node id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write node id: %w", err)
	}
	return id, nil
}

// DefaultNodeIDPath is the canonical location under the working directory.
func DefaultNodeIDPath() string {
	return nodeIDFile
}
```

- [ ] **Step 2:** Create `internal/api/node_id_test.go`:

```go
package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateNodeIDCreatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node_id")

	id1, err := LoadOrCreateNodeID(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(id1) != 32 {
		t.Fatalf("want 32-char hex id, got %d", len(id1))
	}

	id2, err := LoadOrCreateNodeID(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("must persist across calls: %s vs %s", id1, id2)
	}
}

func TestLoadOrCreateNodeIDRegeneratesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node_id")
	if err := os.WriteFile(p, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateNodeID(p)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("want 32-char id after regeneration, got %d", len(id))
	}
}
```

- [ ] **Step 3:** Run `go test ./internal/api/... -v`
- [ ] **Step 4:** `gofmt -w internal/api/`
- [ ] **Step 5:** Commit: `git add internal/api/node_id.go internal/api/node_id_test.go && git commit -m "api: add persistent node_id with on-disk state"`

---

## Task 5: Auto-scaffold `api:` block in first-run config

**Files:**
- Modify: `internal/config/config.go` (function `CheckAndCreateConfigFiles`)

- [ ] **Step 1:** Read current `CheckAndCreateConfigFiles` body; locate the default config.yaml literal (inside `files` map). Extend it with a commented `api:` section that includes a freshly-generated token at scaffold time. Replace the generation logic so the default template is not static — instead, generate token and interpolate.

Change approach: split into build-default function.

Add to `internal/config/config.go`:

```go
import (
	"crypto/rand"
	"encoding/hex"
)

func generateAPIToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "PLEASE_SET_A_64_CHAR_HEX_TOKEN"
	}
	return hex.EncodeToString(b[:])
}
```

- [ ] **Step 2:** In the default `configs/config.yaml` string literal, **append before the bots section** (after the `global:` block):

```yaml

# Panel API (WebSocket). Set enabled: true to turn on, then restart.
# Bind defaults to 127.0.0.1. Expose via cloudflared for wss://.
# api:
#   enabled: false
#   node_name: "my-node"
#   bind_addr: "127.0.0.1:7766"
#   auth_token: "REPLACE_WITH_GENERATED_TOKEN"
#   tls_cert_file: ""
#   tls_key_file: ""
#   event_buffer: 1000
#   max_connections: 4
```

Replace `REPLACE_WITH_GENERATED_TOKEN` at write time by `strings.Replace(defaultYAML, "REPLACE_WITH_GENERATED_TOKEN", generateAPIToken(), 1)`.

- [ ] **Step 3:** `go build ./...` — ensure still compiles.
- [ ] **Step 4:** Manual check: remove `configs/config.yaml` in a temp copy, run `./gNb` (it should recreate and exit). Skip in CI — just confirm build.

Actually simpler verification: `go test ./internal/config/...` (even if no new test — old tests should pass).

- [ ] **Step 5:** `gofmt -w internal/config/`
- [ ] **Step 6:** Commit: `git add internal/config/config.go && git commit -m "api: auto-scaffold commented api block with generated token"`

---

## Task 6: Wire protocol envelope types + JSON tests

**Files:**
- Create: `internal/api/protocol.go`
- Create: `internal/api/protocol_test.go`

- [ ] **Step 1:** Create `internal/api/protocol.go`:

```go
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
	Type   string      `json:"type"`
	ID     string      `json:"id"`
	OK     bool        `json:"ok"`
	Result interface{} `json:"result"`
}

type ErrorMsg struct {
	Type    string    `json:"type"`
	ID      string    `json:"id,omitempty"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

type EventMsg struct {
	Type   string      `json:"type"`
	Event  string      `json:"event"`
	NodeID string      `json:"node_id"`
	TS     string      `json:"ts"`
	Seq    uint64      `json:"seq"`
	Data   interface{} `json:"data"`
}

func NewResponse(id string, result interface{}) ResponseMsg {
	return ResponseMsg{Type: "response", ID: id, OK: true, Result: result}
}

func NewError(id string, code ErrorCode, msg string) ErrorMsg {
	return ErrorMsg{Type: "error", ID: id, Code: code, Message: msg}
}

func NewEvent(nodeID, event string, seq uint64, ts string, data interface{}) EventMsg {
	return EventMsg{Type: "event", Event: event, NodeID: nodeID, Seq: seq, TS: ts, Data: data}
}

// DecodeEnvelope returns the parsed request and an error if the message is not a valid inbound envelope.
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
```

- [ ] **Step 2:** Create `internal/api/protocol_test.go`:

```go
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
```

- [ ] **Step 3:** `go test ./internal/api/... -run 'Decode|Response|Event' -v`
- [ ] **Step 4:** `gofmt -w internal/api/`
- [ ] **Step 5:** Commit: `git add internal/api/protocol.go internal/api/protocol_test.go && git commit -m "api: add wire envelope types and decode helpers"`

---

## Task 7: `EventHub` with ring buffer + fan-out

**Files:**
- Create: `internal/api/events.go`
- Create: `internal/api/events_test.go`

- [ ] **Step 1:** Create `internal/api/events.go`:

```go
package api

import (
	"sync"
	"sync/atomic"
	"time"
)

// EventHub stores a ring buffer of EventMsg and fans out new events to
// registered subscribers. Safe for concurrent use.
type EventHub struct {
	nodeID string
	bufMu  sync.Mutex
	buf    []EventMsg
	bufCap int
	seq    atomic.Uint64

	subMu sync.RWMutex
	subs  map[uint64]*Subscriber
	nextSubID uint64
}

type Subscriber struct {
	id       uint64
	ch       chan EventMsg
	topics   map[string]struct{} // nil = all
	dropped  atomic.Uint64
	backWarn atomic.Bool
}

func NewEventHub(nodeID string, bufferSize int) *EventHub {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &EventHub{
		nodeID: nodeID,
		buf:    make([]EventMsg, 0, bufferSize),
		bufCap: bufferSize,
		subs:   make(map[uint64]*Subscriber),
	}
}

// Publish appends the event to the ring buffer and fans out to matching subscribers.
func (h *EventHub) Publish(event string, data interface{}) EventMsg {
	s := h.seq.Add(1)
	msg := EventMsg{
		Type:   "event",
		Event:  event,
		NodeID: h.nodeID,
		TS:     time.Now().UTC().Format(time.RFC3339Nano),
		Seq:    s,
		Data:   data,
	}

	h.bufMu.Lock()
	if len(h.buf) == h.bufCap {
		copy(h.buf, h.buf[1:])
		h.buf = h.buf[:h.bufCap-1]
	}
	h.buf = append(h.buf, msg)
	h.bufMu.Unlock()

	h.subMu.RLock()
	for _, sub := range h.subs {
		if !sub.topicMatch(event) {
			continue
		}
		select {
		case sub.ch <- msg:
		default:
			sub.dropped.Add(1)
		}
	}
	h.subMu.RUnlock()
	return msg
}

// Subscribe registers a new subscriber. topics=nil means all. bufferSize is the
// outbound channel buffer length.
func (h *EventHub) Subscribe(topics []string, bufferSize int) *Subscriber {
	var topicSet map[string]struct{}
	if topics != nil {
		topicSet = make(map[string]struct{}, len(topics))
		for _, t := range topics {
			topicSet[t] = struct{}{}
		}
	}
	sub := &Subscriber{
		ch:     make(chan EventMsg, bufferSize),
		topics: topicSet,
	}

	h.subMu.Lock()
	h.nextSubID++
	sub.id = h.nextSubID
	h.subs[sub.id] = sub
	h.subMu.Unlock()
	return sub
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *EventHub) Unsubscribe(sub *Subscriber) {
	h.subMu.Lock()
	if _, ok := h.subs[sub.id]; ok {
		delete(h.subs, sub.id)
		close(sub.ch)
	}
	h.subMu.Unlock()
}

// Replay returns up to n most recent matching events from the ring buffer.
func (h *EventHub) Replay(sub *Subscriber, n int) []EventMsg {
	if n <= 0 {
		return nil
	}
	h.bufMu.Lock()
	defer h.bufMu.Unlock()

	out := make([]EventMsg, 0, n)
	for i := len(h.buf) - 1; i >= 0 && len(out) < n; i-- {
		if sub.topicMatch(h.buf[i].Event) {
			out = append([]EventMsg{h.buf[i]}, out...)
		}
	}
	return out
}

// Seq returns the current highest seq number.
func (h *EventHub) Seq() uint64 { return h.seq.Load() }

// Ch returns the subscriber's outbound channel.
func (s *Subscriber) Ch() <-chan EventMsg { return s.ch }

// Dropped returns the count of events dropped due to backpressure.
func (s *Subscriber) Dropped() uint64 { return s.dropped.Load() }

func (s *Subscriber) topicMatch(event string) bool {
	if s.topics == nil {
		return true
	}
	_, ok := s.topics[event]
	return ok
}
```

- [ ] **Step 2:** Create `internal/api/events_test.go`:

```go
package api

import (
	"sync"
	"testing"
	"time"
)

func TestEventHubPublishAndSubscribe(t *testing.T) {
	h := NewEventHub("n1", 10)
	sub := h.Subscribe(nil, 4)
	defer h.Unsubscribe(sub)

	h.Publish("bot.connected", map[string]string{"bot_id": "a"})
	select {
	case msg := <-sub.Ch():
		if msg.Event != "bot.connected" {
			t.Fatalf("want bot.connected, got %s", msg.Event)
		}
		if msg.Seq != 1 {
			t.Fatalf("want seq=1, got %d", msg.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventHubTopicFilter(t *testing.T) {
	h := NewEventHub("n1", 10)
	sub := h.Subscribe([]string{"bot.connected"}, 4)
	defer h.Unsubscribe(sub)

	h.Publish("bot.disconnected", nil) // must NOT deliver
	h.Publish("bot.connected", nil)    // must deliver

	select {
	case msg := <-sub.Ch():
		if msg.Event != "bot.connected" {
			t.Fatalf("filter broken, got %s", msg.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEventHubRingBufferEviction(t *testing.T) {
	h := NewEventHub("n1", 3)
	for i := 0; i < 5; i++ {
		h.Publish("e", i)
	}
	sub := h.Subscribe(nil, 0) // buffer 0 so Replay is the only path
	defer h.Unsubscribe(sub)

	got := h.Replay(sub, 10)
	if len(got) != 3 {
		t.Fatalf("want 3 events retained, got %d", len(got))
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("ring order wrong: %+v", got)
	}
}

func TestEventHubBackpressureDrop(t *testing.T) {
	h := NewEventHub("n1", 10)
	sub := h.Subscribe(nil, 1)
	defer h.Unsubscribe(sub)

	h.Publish("a", nil)
	h.Publish("b", nil) // second one drops (chan full, no consumer)
	h.Publish("c", nil)

	if got := sub.Dropped(); got < 2 {
		t.Fatalf("want >=2 drops, got %d", got)
	}
}

func TestEventHubConcurrentPublish(t *testing.T) {
	h := NewEventHub("n1", 1000)
	sub := h.Subscribe(nil, 1000)
	defer h.Unsubscribe(sub)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.Publish("x", nil)
			}
		}()
	}
	wg.Wait()

	if h.Seq() != 500 {
		t.Fatalf("want seq=500, got %d", h.Seq())
	}
}
```

- [ ] **Step 3:** `go test -race ./internal/api/... -run EventHub -v`
- [ ] **Step 4:** `gofmt -w internal/api/`
- [ ] **Step 5:** Commit: `git add internal/api/events.go internal/api/events_test.go && git commit -m "api: add EventHub with ring buffer, topic filter, and backpressure drop"`

---

## Task 8: `EventSink` interface in types + stub implementation

**Files:**
- Modify: `internal/types/interfaces.go`

- [ ] **Step 1:** Append to `internal/types/interfaces.go`:

```go
// EventSink is the outbound channel through which Bot / BotManager /
// NickManager notify external observers (currently the Panel API) about
// lifecycle changes. Bot internals must call methods on this interface only
// when non-nil. Implementations must not block the caller.
type EventSink interface {
	BotConnected(botID, nick, server string)
	BotDisconnected(botID, reason string)
	BotNickChanged(botID, oldNick, newNick string)
	BotNickCaptured(botID, nick, kind string)
	BotJoinedChannel(botID, channel string)
	BotPartedChannel(botID, channel string)
	BotKicked(botID, channel, by, reason string)
	BotBanned(botID string, code int)
	BotAdded(botID, server string, port int, ssl bool, vhost string)
	BotRemoved(botID string)
	NicksChanged(nicks []string)
	OwnersChanged(owners []string)

	// BotIRCEvent delivers every IRC event a bot sees; the API routes it to
	// sessions attached to this bot_id. Payload must not be retained past the
	// call.
	BotIRCEvent(botID string, e *ircEvent)
}

// ircEvent is the narrow slice of *irc.Event the sink needs. We avoid
// importing go-ircevo into types to keep the dependency graph clean. Bot
// constructs these on the fly from *irc.Event.
type ircEvent interface {
	Code() string
	Nick() string
	User() string
	Host() string
	Source() string
	Arguments() []string
	Message() string
	Raw() string
}
```

Actually simpler — since `types/interfaces.go` already imports `irc "github.com/kofany/go-ircevo"`, we can use the real type. Replace the above `ircEvent` interface with the irc package import.

Redo step 1: append the EventSink interface and drop the ircEvent wrapper:

```go
type EventSink interface {
	BotConnected(botID, nick, server string)
	BotDisconnected(botID, reason string)
	BotNickChanged(botID, oldNick, newNick string)
	BotNickCaptured(botID, nick, kind string)
	BotJoinedChannel(botID, channel string)
	BotPartedChannel(botID, channel string)
	BotKicked(botID, channel, by, reason string)
	BotBanned(botID string, code int)
	BotAdded(botID, server string, port int, ssl bool, vhost string)
	BotRemoved(botID string)
	NicksChanged(nicks []string)
	OwnersChanged(owners []string)
	BotIRCEvent(botID string, e *irc.Event)
}
```

- [ ] **Step 2:** Add `SetEventSink(sink EventSink)` method to the `BotManager` interface — same file. Implementation added in Task 19.

```go
type BotManager interface {
	// ... existing methods ...
	SetEventSink(sink EventSink)
}
```

- [ ] **Step 3:** `go build ./...` — will fail because `*BotManager` does not implement `SetEventSink`. That's expected; Task 19 adds the concrete impl. For now, just the interface.

Actually this will break any code that uses BotManager. Defer the SetEventSink method addition until Task 19 — put the EventSink interface in place here but don't require it on BotManager yet.

**Revised Step 2:** skip adding SetEventSink to BotManager interface. Just add the EventSink interface definition.

- [ ] **Step 4:** `go build ./...` — must succeed (unused new interface is fine).
- [ ] **Step 5:** `gofmt -w internal/types/`
- [ ] **Step 6:** Commit: `git add internal/types/interfaces.go && git commit -m "api: add EventSink interface for bot lifecycle/attach events"`

---

## Task 9: API server skeleton (HTTP + WS upgrade + auth gate)

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/auth.go`
- Create: `internal/api/auth_test.go`
- Create: `internal/api/server_test.go`

- [ ] **Step 1:** Create `internal/api/auth.go`:

```go
package api

import (
	"crypto/subtle"
	"sync"
	"time"
)

// tokenChecker does constant-time comparison and per-IP rate limiting.
type tokenChecker struct {
	expected []byte

	mu      sync.Mutex
	fails   map[string][]time.Time
	window  time.Duration
	maxFail int
}

func newTokenChecker(expected string) *tokenChecker {
	return &tokenChecker{
		expected: []byte(expected),
		fails:    make(map[string][]time.Time),
		window:   60 * time.Second,
		maxFail:  5,
	}
}

// CheckToken reports ok=true if token matches and the remote is not rate-limited.
// If ok=false, rateLimited=true indicates the caller exceeded the attempt limit.
func (c *tokenChecker) CheckToken(remote, token string) (ok, rateLimited bool) {
	c.mu.Lock()
	now := time.Now()
	times := c.fails[remote]
	cutoff := now.Add(-c.window)
	// prune
	pruned := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	if len(pruned) >= c.maxFail {
		c.fails[remote] = pruned
		c.mu.Unlock()
		return false, true
	}
	c.mu.Unlock()

	if subtle.ConstantTimeCompare([]byte(token), c.expected) == 1 {
		c.mu.Lock()
		delete(c.fails, remote)
		c.mu.Unlock()
		return true, false
	}

	c.mu.Lock()
	c.fails[remote] = append(pruned, now)
	c.mu.Unlock()
	return false, false
}
```

- [ ] **Step 2:** Create `internal/api/auth_test.go`:

```go
package api

import "testing"

func TestTokenCheckerMatches(t *testing.T) {
	c := newTokenChecker("supersecret")
	ok, rl := c.CheckToken("1.2.3.4", "supersecret")
	if !ok || rl {
		t.Fatalf("want ok=true, got ok=%v rl=%v", ok, rl)
	}
}

func TestTokenCheckerRejectsWrong(t *testing.T) {
	c := newTokenChecker("supersecret")
	ok, rl := c.CheckToken("1.2.3.4", "nope")
	if ok || rl {
		t.Fatalf("want ok=false, rl=false, got ok=%v rl=%v", ok, rl)
	}
}

func TestTokenCheckerRateLimits(t *testing.T) {
	c := newTokenChecker("x")
	for i := 0; i < 5; i++ {
		c.CheckToken("1.1.1.1", "bad")
	}
	_, rl := c.CheckToken("1.1.1.1", "bad")
	if !rl {
		t.Fatalf("want rate-limited after 5 failures")
	}
	// Other IP still unlocked.
	_, rl2 := c.CheckToken("2.2.2.2", "bad")
	if rl2 {
		t.Fatalf("rate limit must be per-IP")
	}
}
```

- [ ] **Step 3:** Create `internal/api/server.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Deps bundles everything the API handlers need from the rest of the program.
type Deps struct {
	Config      *config.Config
	BotManager  types.BotManager
	NickManager types.NickManager
}

// Server is the panel WebSocket API server.
type Server struct {
	cfg       config.APIConfig
	nodeID    string
	deps      Deps
	hub       *EventHub
	attach    *AttachManager
	auth      *tokenChecker
	startTime time.Time

	httpSrv *http.Server

	sessMu     sync.Mutex
	sessions   map[uint64]*Session
	nextSessID uint64
	activeN    atomic.Int32
}

// New builds a Server but does not start it.
func New(cfg config.APIConfig, nodeID string, deps Deps) *Server {
	s := &Server{
		cfg:       cfg,
		nodeID:    nodeID,
		deps:      deps,
		hub:       NewEventHub(nodeID, cfg.EventBuffer),
		auth:      newTokenChecker(cfg.AuthToken),
		startTime: time.Now(),
		sessions:  make(map[uint64]*Session),
	}
	s.attach = NewAttachManager()
	return s
}

// Hub exposes the EventHub (used by BotManager/Bot to publish events).
func (s *Server) Hub() *EventHub { return s.hub }

// Attach exposes the AttachManager.
func (s *Server) AttachMgr() *AttachManager { return s.attach }

// Run starts the HTTP listener and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	s.httpSrv = &http.Server{
		Addr:              s.cfg.BindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
			util.Info("API: starting WSS server on %s (TLS)", s.cfg.BindAddr)
			errCh <- s.httpSrv.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		} else {
			util.Info("API: starting WS server on %s (plain HTTP)", s.cfg.BindAddr)
			errCh <- s.httpSrv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
		s.closeAllSessions(websocket.StatusGoingAway, "shutdown")
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("api server: %w", err)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.activeN.Load() >= int32(s.cfg.MaxConnections) {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // we gate via token, not origin
	})
	if err != nil {
		util.Warning("API: websocket accept failed: %v", err)
		return
	}
	c.SetReadLimit(64 * 1024)

	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	if remote == "" {
		remote = r.RemoteAddr
	}

	s.activeN.Add(1)
	defer s.activeN.Add(-1)

	s.sessMu.Lock()
	s.nextSessID++
	sid := s.nextSessID
	s.sessMu.Unlock()

	sess := newSession(sid, remote, c, s)

	s.sessMu.Lock()
	s.sessions[sid] = sess
	s.sessMu.Unlock()

	defer func() {
		s.sessMu.Lock()
		delete(s.sessions, sid)
		s.sessMu.Unlock()
		sess.close()
	}()

	sess.run(r.Context())
}

func (s *Server) closeAllSessions(code websocket.StatusCode, reason string) {
	s.sessMu.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.sessMu.Unlock()
	for _, sess := range sessions {
		sess.conn.Close(code, reason)
	}
}
```

- [ ] **Step 4:** Stub `internal/api/attach.go` (real impl in Task 24):

```go
package api

import "sync"

type AttachManager struct {
	mu   sync.RWMutex
	subs map[string]map[uint64]struct{} // bot_id -> session_id set
}

func NewAttachManager() *AttachManager {
	return &AttachManager{subs: make(map[string]map[uint64]struct{})}
}

func (am *AttachManager) Attach(botID string, sessionID uint64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	set, ok := am.subs[botID]
	if !ok {
		set = make(map[uint64]struct{})
		am.subs[botID] = set
	}
	set[sessionID] = struct{}{}
}

func (am *AttachManager) Detach(botID string, sessionID uint64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if set, ok := am.subs[botID]; ok {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(am.subs, botID)
		}
	}
}

func (am *AttachManager) DetachAll(sessionID uint64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	for botID, set := range am.subs {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(am.subs, botID)
		}
	}
}

func (am *AttachManager) Sessions(botID string) []uint64 {
	am.mu.RLock()
	defer am.mu.RUnlock()
	set, ok := am.subs[botID]
	if !ok {
		return nil
	}
	out := make([]uint64, 0, len(set))
	for sid := range set {
		out = append(out, sid)
	}
	return out
}
```

- [ ] **Step 5:** `go build ./internal/api/...` must succeed even though `Session.run` / `newSession` don't exist yet — they come in Task 11. For this task we stop here and verify the auth test passes.

Actually for `go build ./...` to succeed after this task we need Session to exist. Reorder: Task 11 (session) must come before Task 9 runs clean build. Decision: make Task 9 and Task 11 a tight pair — write Task 9 code but gate the build until Task 11 completes.

**Revised approach:** this task adds `auth.go`, `auth_test.go`, and the `AttachManager` stub. Server is Task 11.

Revise Step 3 and onwards: drop server.go and closeAllSessions here; only land auth + attach stub in this task.

**Revised Step 3:** Skip.
**Revised Step 4:** Create `internal/api/attach.go` (code above).
**Revised Step 5:** `go test ./internal/api/... -run Token -v`
- [ ] **Step 6:** `gofmt -w internal/api/`
- [ ] **Step 7:** Commit: `git add internal/api/auth.go internal/api/auth_test.go internal/api/attach.go && git commit -m "api: add token checker with rate limit and AttachManager registry"`

---

## Task 10: Session + Server with WS upgrade, handshake, dispatch pump

**Files:**
- Create: `internal/api/session.go`
- Create: `internal/api/server.go`
- Create: `internal/api/dispatch.go`
- Create: `internal/api/server_test.go`

- [ ] **Step 1:** Create `internal/api/dispatch.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// Handler processes a decoded request and returns a result or error.
// The session is provided for per-session state (subscribe/attach).
type Handler func(ctx context.Context, s *Session, req *RequestMsg) (result interface{}, err *HandlerError)

type HandlerError struct {
	Code    ErrorCode
	Message string
}

func (e *HandlerError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func paramErr(format string, args ...interface{}) *HandlerError {
	return &HandlerError{Code: ErrInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func notFound(msg string) *HandlerError { return &HandlerError{Code: ErrNotFound, Message: msg} }

func internalErr(err error) *HandlerError {
	return &HandlerError{Code: ErrInternal, Message: err.Error()}
}

// Router maps method names to handlers.
type Router struct {
	handlers map[string]Handler
}

func NewRouter() *Router { return &Router{handlers: make(map[string]Handler)} }

func (r *Router) Register(method string, h Handler) { r.handlers[method] = h }

func (r *Router) Dispatch(ctx context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	// auth.login is the only method allowed pre-auth.
	if !s.isAuthed() && req.Method != "auth.login" {
		return nil, &HandlerError{Code: ErrForbidden, Message: "not authenticated"}
	}
	h, ok := r.handlers[req.Method]
	if !ok {
		return nil, &HandlerError{Code: ErrUnknownMethod, Message: "unknown method: " + req.Method}
	}
	return h(ctx, s, req)
}

// decodeParams unmarshals req.Params into dst. Returns paramErr on failure.
func decodeParams(req *RequestMsg, dst interface{}) *HandlerError {
	if len(req.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Params, dst); err != nil {
		return paramErr("invalid params: %v", err)
	}
	return nil
}
```

- [ ] **Step 2:** Create `internal/api/session.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/kofany/gNb/internal/util"
)

const (
	handshakeDeadline = 5 * time.Second
	readIdleDeadline  = 45 * time.Second
	pingInterval      = 30 * time.Second
	outboundBuffer    = 256
)

// Session is the per-connection state.
type Session struct {
	id     uint64
	remote string
	conn   *websocket.Conn
	server *Server

	authed atomic.Bool

	out chan outboundMsg
	sub *Subscriber // nil until events.subscribe

	// attached bot_ids for cleanup on close.
	attachedMu sync.Mutex
	attached   map[string]struct{}
}

type outboundMsg struct {
	payload interface{}
}

func newSession(id uint64, remote string, c *websocket.Conn, s *Server) *Session {
	return &Session{
		id:       id,
		remote:   remote,
		conn:     c,
		server:   s,
		out:      make(chan outboundMsg, outboundBuffer),
		attached: make(map[string]struct{}),
	}
}

func (s *Session) isAuthed() bool { return s.authed.Load() }

func (s *Session) markAuthed() { s.authed.Store(true) }

// ID returns the session's numeric id (used by AttachManager).
func (s *Session) ID() uint64 { return s.id }

// Server returns the owning Server (handlers need it for Deps).
func (s *Session) Server() *Server { return s.server }

func (s *Session) trackAttach(botID string) {
	s.attachedMu.Lock()
	s.attached[botID] = struct{}{}
	s.attachedMu.Unlock()
}

func (s *Session) untrackAttach(botID string) {
	s.attachedMu.Lock()
	delete(s.attached, botID)
	s.attachedMu.Unlock()
}

func (s *Session) listAttached() []string {
	s.attachedMu.Lock()
	out := make([]string, 0, len(s.attached))
	for k := range s.attached {
		out = append(out, k)
	}
	s.attachedMu.Unlock()
	return out
}

// close handles cleanup: detach from attach manager, unsubscribe from hub.
func (s *Session) close() {
	for _, bid := range s.listAttached() {
		s.server.attach.Detach(bid, s.id)
	}
	if s.sub != nil {
		s.server.hub.Unsubscribe(s.sub)
		s.sub = nil
	}
}

// send enqueues a message to the outbound writer. Non-blocking; drops on overflow.
func (s *Session) send(msg interface{}) {
	select {
	case s.out <- outboundMsg{payload: msg}:
	default:
		// outbound full — cannot deliver. Close the session.
		s.conn.Close(websocket.StatusPolicyViolation, "backpressure")
	}
}

// sendEvent converts EventMsg and enqueues. Called from hub subscriber pump.
func (s *Session) sendEvent(msg EventMsg) { s.send(msg) }

// run drives the handshake + read loop + write pump.
func (s *Session) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan struct{})
	go s.writerLoop(ctx, done)

	if !s.handshake(ctx) {
		cancel()
		<-done
		return
	}
	s.readerLoop(ctx)
	cancel()
	<-done
}

// handshake waits for auth.login with a deadline, validates token, and
// responds. Returns true if the session is authenticated.
func (s *Session) handshake(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, handshakeDeadline)
	defer cancel()

	_, raw, err := s.conn.Read(ctx)
	if err != nil {
		s.conn.Close(4003, "handshake timeout")
		return false
	}
	req, err := DecodeRequest(raw)
	if err != nil {
		s.send(NewError("", ErrInvalidParams, err.Error()))
		s.conn.Close(4004, "protocol error")
		return false
	}
	if req.Method != "auth.login" {
		s.send(NewError(req.ID, ErrForbidden, "first message must be auth.login"))
		s.conn.Close(4001, "not authenticated")
		return false
	}
	var p struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.send(NewError(req.ID, ErrInvalidParams, "missing token"))
		s.conn.Close(4001, "bad token")
		return false
	}
	ok, rl := s.server.auth.CheckToken(s.remote, p.Token)
	if rl {
		s.send(NewError(req.ID, ErrRateLimited, "too many failed attempts"))
		s.conn.Close(4001, "rate limited")
		return false
	}
	if !ok {
		s.send(NewError(req.ID, ErrUnauthorized, "invalid token"))
		s.conn.Close(4001, "unauthorized")
		return false
	}
	s.markAuthed()
	s.send(NewResponse(req.ID, map[string]interface{}{
		"node_id":     s.server.nodeID,
		"node_name":   s.server.cfg.NodeName,
		"api_version": "1.0",
		"session_id":  s.id,
	}))
	util.Info("API: session %d authenticated from %s", s.id, s.remote)
	return true
}

func (s *Session) readerLoop(ctx context.Context) {
	for {
		rctx, cancel := context.WithTimeout(ctx, readIdleDeadline)
		_, raw, err := s.conn.Read(rctx)
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				util.Debug("API: session %d read: %v", s.id, err)
			}
			return
		}
		req, derr := DecodeRequest(raw)
		if derr != nil {
			s.send(NewError("", ErrInvalidParams, derr.Error()))
			continue
		}
		result, herr := s.server.router.Dispatch(ctx, s, req)
		if herr != nil {
			s.send(NewError(req.ID, herr.Code, herr.Message))
			continue
		}
		s.send(NewResponse(req.ID, result))
	}
}

func (s *Session) writerLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	pingT := time.NewTicker(pingInterval)
	defer pingT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-s.out:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(wctx, s.conn, msg.payload)
			cancel()
			if err != nil {
				s.conn.Close(websocket.StatusInternalError, "write failed")
				return
			}
		case <-pingT.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
```

Add `"sync"` to imports.

- [ ] **Step 3:** Create `internal/api/server.go` (full impl from Task 9 Step 3):

Copy the server.go source from Task 9 Step 3 into `internal/api/server.go`, then add a `router *Router` field to `Server`:

```go
type Server struct {
	cfg       config.APIConfig
	nodeID    string
	deps      Deps
	hub       *EventHub
	attach    *AttachManager
	auth      *tokenChecker
	router    *Router
	startTime time.Time

	httpSrv *http.Server

	sessMu     sync.Mutex
	sessions   map[uint64]*Session
	nextSessID uint64
	activeN    atomic.Int32
}
```

And in `New`, initialize `s.router = NewRouter()`.

- [ ] **Step 4:** Create `internal/api/server_test.go` (E2E smoke; real WS client):

```go
package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/kofany/gNb/internal/config"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := config.APIConfig{
		Enabled:        true,
		AuthToken:      strings.Repeat("x", 32),
		BindAddr:       "127.0.0.1:0",
		EventBuffer:    100,
		MaxConnections: 4,
		NodeName:       "test-node",
	}
	cfg.ApplyDefaults()
	s := New(cfg, "testnode", Deps{})
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	return s, ts
}

func wsDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(url, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func TestHandshakeSuccess(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	req := map[string]interface{}{
		"type":   "request",
		"id":     "1",
		"method": "auth.login",
		"params": map[string]string{"token": strings.Repeat("x", 32)},
	}
	if err := wsjson.Write(ctx, c, req); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := wsjson.Read(rctx, c, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "response" || got["ok"] != true {
		b, _ := json.Marshal(got)
		t.Fatalf("bad response: %s", b)
	}
}

func TestHandshakeBadToken(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	req := map[string]interface{}{
		"type":   "request",
		"id":     "1",
		"method": "auth.login",
		"params": map[string]string{"token": "wrong"},
	}
	_ = wsjson.Write(ctx, c, req)
	var got map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &got)
	if got["type"] != "error" {
		t.Fatalf("want error, got %+v", got)
	}
	if got["code"] != "unauthorized" {
		t.Fatalf("want code=unauthorized, got %v", got["code"])
	}
}

func TestHandshakeTimeout(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	// Don't send anything; server should close within ~5s.
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	var got map[string]interface{}
	err := wsjson.Read(ctx, c, &got)
	if err == nil {
		t.Fatalf("want close, got message: %+v", got)
	}
}
```

Add `"net/http"` to imports in the test file.

- [ ] **Step 5:** Run: `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 6:** `gofmt -w internal/api/`
- [ ] **Step 7:** Commit: `git add internal/api && git commit -m "api: add WebSocket server skeleton with auth handshake and reader/writer loops"`

---

## Task 11: `auth.login` + `node.info` handlers

**Files:**
- Create: `internal/api/handlers_core.go`
- Modify: `internal/api/server.go` (register handlers)

- [ ] **Step 1:** Create `internal/api/handlers_core.go`:

```go
package api

import (
	"context"
	"time"
)

// handleAuthLogin is a no-op response handler: the actual token check happens
// in Session.handshake before dispatch. Registering the method here means
// subsequent re-login requests (e.g. token rotation) are recognized and
// rejected gracefully as already-authenticated.
func handleAuthLogin(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	return nil, &HandlerError{Code: ErrForbidden, Message: "already authenticated"}
}

func handleNodeInfo(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	srv := s.server
	bots := srv.deps.BotManager.GetBots()
	connected := 0
	for _, b := range bots {
		if b.IsConnected() {
			connected++
		}
	}
	return map[string]interface{}{
		"node_id":             srv.nodeID,
		"node_name":           srv.cfg.NodeName,
		"api_version":         "1.0",
		"uptime_seconds":      int64(time.Since(srv.startTime).Seconds()),
		"num_bots":            len(bots),
		"num_connected_bots":  connected,
		"started_at":          srv.startTime.UTC().Format(time.RFC3339),
	}, nil
}
```

- [ ] **Step 2:** In `server.go`, add a `registerRoutes()` method and call it from `New`:

```go
func (s *Server) registerRoutes() {
	s.router.Register("auth.login", handleAuthLogin)
	s.router.Register("node.info", handleNodeInfo)
}
```

In `New`, after `s.router = NewRouter()`, add `s.registerRoutes()`.

- [ ] **Step 3:** Extend `server_test.go` with:

```go
func TestNodeInfoRequiresAuth(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	req := map[string]interface{}{"type": "request", "id": "1", "method": "node.info"}
	_ = wsjson.Write(ctx, c, req)
	var got map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &got)
	if got["type"] != "error" || got["code"] != "forbidden" {
		t.Fatalf("want forbidden, got %+v", got)
	}
}
```

- [ ] **Step 4:** Node.info depends on BotManager. For the test we need a fake BotManager. Create `internal/api/fakes_test.go`:

```go
package api

import (
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/types"
	irc "github.com/kofany/go-ircevo"
)

type fakeBot struct {
	id        string
	nick      string
	server    string
	connected bool
	channels  []string
}

func (f *fakeBot) AttemptNickChange(string)               {}
func (f *fakeBot) GetCurrentNick() string                 { return f.nick }
func (f *fakeBot) IsConnected() bool                      { return f.connected }
func (f *fakeBot) IsOnChannel(ch string) bool {
	for _, c := range f.channels {
		if c == ch {
			return true
		}
	}
	return false
}
func (f *fakeBot) SetOwnerList(auth.OwnerList)                       {}
func (f *fakeBot) SetChannels([]string)                              {}
func (f *fakeBot) RequestISON([]string) ([]string, error)            { return nil, nil }
func (f *fakeBot) Connect() error                                    { return nil }
func (f *fakeBot) Quit(string)                                       {}
func (f *fakeBot) Reconnect()                                        {}
func (f *fakeBot) SendMessage(string, string)                        {}
func (f *fakeBot) JoinChannel(string)                                {}
func (f *fakeBot) PartChannel(string)                                {}
func (f *fakeBot) ChangeNick(string)                                 {}
func (f *fakeBot) HandleCommands(*irc.Event)                         {}
func (f *fakeBot) SetBotManager(types.BotManager)                    {}
func (f *fakeBot) GetBotManager() types.BotManager                   { return nil }
func (f *fakeBot) SetNickManager(types.NickManager)                  {}
func (f *fakeBot) GetNickManager() types.NickManager                 { return nil }
func (f *fakeBot) GetServerName() string                             { return f.server }
func (f *fakeBot) StartBNC() (int, string, error)                    { return 0, "", nil }
func (f *fakeBot) StopBNC()                                          {}
func (f *fakeBot) SendRaw(string)                                    {}
func (f *fakeBot) RemoveBot()                                        {}

type fakeBotManager struct {
	bots   []types.Bot
	owners []string
}

func (f *fakeBotManager) StartBots()                              {}
func (f *fakeBotManager) Stop()                                   {}
func (f *fakeBotManager) CanExecuteMassCommand(string) bool        { return true }
func (f *fakeBotManager) AddOwner(string) error                    { return nil }
func (f *fakeBotManager) RemoveOwner(string) error                 { return nil }
func (f *fakeBotManager) GetOwners() []string                     { return f.owners }
func (f *fakeBotManager) GetBots() []types.Bot                    { return f.bots }
func (f *fakeBotManager) GetNickManager() types.NickManager       { return nil }
func (f *fakeBotManager) GetTotalCreatedBots() int                 { return len(f.bots) }
func (f *fakeBotManager) SetMassCommandCooldown(time.Duration)    {}
func (f *fakeBotManager) GetMassCommandCooldown() time.Duration   { return 0 }
func (f *fakeBotManager) CollectReactions(string, string, func() error) {}
func (f *fakeBotManager) SendSingleMsg(string, string)            {}

type fakeNickManager struct {
	nicks []string
}

func (f *fakeNickManager) RegisterBot(types.Bot)                            {}
func (f *fakeNickManager) ReturnNickToPool(string)                           {}
func (f *fakeNickManager) SetBots([]types.Bot)                              {}
func (f *fakeNickManager) GetNicksToCatch() []string                         { return f.nicks }
func (f *fakeNickManager) AddNick(string) error                              { return nil }
func (f *fakeNickManager) RemoveNick(string) error                           { return nil }
func (f *fakeNickManager) GetNicks() []string                                { return f.nicks }
func (f *fakeNickManager) MarkNickAsTemporarilyUnavailable(string)           {}
func (f *fakeNickManager) NotifyNickChange(string, string)                   {}
func (f *fakeNickManager) MarkServerNoLetters(string)                        {}
func (f *fakeNickManager) Start()                                            {}
func (f *fakeNickManager) Stop()                                             {}
```

Update `testServer` in `server_test.go` to inject fakes:

```go
func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := config.APIConfig{
		Enabled:        true,
		AuthToken:      strings.Repeat("x", 32),
		BindAddr:       "127.0.0.1:0",
		EventBuffer:    100,
		MaxConnections: 4,
		NodeName:       "test-node",
	}
	cfg.ApplyDefaults()
	s := New(cfg, "testnode", Deps{
		BotManager:  &fakeBotManager{},
		NickManager: &fakeNickManager{},
	})
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	return s, ts
}
```

Add a helper `authed(t, url)` that dials + completes handshake and returns the conn:

```go
func authed(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	c := wsDial(t, ts.URL)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "login", "method": "auth.login",
		"params": map[string]string{"token": strings.Repeat("x", 32)},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("handshake failed: %+v", resp)
	}
	return c
}
```

Add a test that calls node.info after auth:

```go
func TestNodeInfoAfterAuth(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type": "request", "id": "2", "method": "node.info"})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("want response, got %+v", resp)
	}
	result := resp["result"].(map[string]interface{})
	if result["node_name"] != "test-node" {
		t.Fatalf("bad result: %+v", result)
	}
}
```

- [ ] **Step 5:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 6:** `gofmt -w internal/api/`
- [ ] **Step 7:** Commit: `git add internal/api && git commit -m "api: implement auth.login + node.info handlers with fake BotManager tests"`

---

## Task 12: `bot.list` + bot_id mapping

**Files:**
- Modify: `internal/api/server.go` (store bot_id map)
- Create: `internal/api/handlers_list.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1:** We need a stable bot_id → Bot mapping. Since bot_id is derived from config+index, the API computes it at startup from `deps.Config.Bots`. Add to Server:

```go
type Server struct {
	// ...existing...
	botIDByIndex []string            // index in cfg.Bots -> bot_id
	indexByID    map[string]int      // bot_id -> index
}
```

In `New`, after field init:

```go
s.botIDByIndex = make([]string, len(deps.Config.Bots))
s.indexByID = make(map[string]int, len(deps.Config.Bots))
for i, bc := range deps.Config.Bots {
	id := ComputeBotID(bc.Server, bc.Port, bc.Vhost, i)
	s.botIDByIndex[i] = id
	s.indexByID[id] = i
}
```

Guard against nil `deps.Config`:

```go
if deps.Config != nil {
	for i, bc := range deps.Config.Bots {
		...
	}
}
```

- [ ] **Step 2:** Add helper methods to Server:

```go
// BotByID returns the Bot at the position mapped by bot_id, or nil.
func (s *Server) BotByID(id string) types.Bot {
	i, ok := s.indexByID[id]
	if !ok {
		return nil
	}
	bots := s.deps.BotManager.GetBots()
	if i >= len(bots) {
		return nil
	}
	return bots[i]
}

// BotIDByIndex returns the bot_id for position i (or "" if out of range).
func (s *Server) BotIDByIndex(i int) string {
	if i < 0 || i >= len(s.botIDByIndex) {
		return ""
	}
	return s.botIDByIndex[i]
}
```

- [ ] **Step 3:** Create `internal/api/handlers_list.go`:

```go
package api

import (
	"context"
)

type BotSummary struct {
	BotID              string   `json:"bot_id"`
	Server             string   `json:"server"`
	Port               int      `json:"port"`
	SSL                bool     `json:"ssl"`
	Vhost              string   `json:"vhost"`
	CurrentNick        string   `json:"current_nick"`
	Connected          bool     `json:"connected"`
	IsSingleLetterNick bool     `json:"is_single_letter_nick"`
	JoinedChannels     []string `json:"joined_channels"`
}

func handleBotList(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	srv := s.server
	bots := srv.deps.BotManager.GetBots()
	cfgBots := srv.deps.Config.Bots
	out := make([]BotSummary, 0, len(bots))
	for i, b := range bots {
		var bc = cfgBots[i]
		nick := b.GetCurrentNick()
		out = append(out, BotSummary{
			BotID:              srv.BotIDByIndex(i),
			Server:             bc.Server,
			Port:               bc.Port,
			SSL:                bc.SSL,
			Vhost:              bc.Vhost,
			CurrentNick:        nick,
			Connected:          b.IsConnected(),
			IsSingleLetterNick: len(nick) == 1,
			JoinedChannels:     joinedChannels(srv.deps.Config.Channels, b),
		})
	}
	return map[string]interface{}{"bots": out}, nil
}

// joinedChannels computes the intersection of configured channels and the
// ones the bot is currently on. Fallback when Bot doesn't expose a joined list.
func joinedChannels(all []string, b interface {
	IsOnChannel(string) bool
}) []string {
	out := []string{}
	for _, ch := range all {
		if b.IsOnChannel(ch) {
			out = append(out, ch)
		}
	}
	return out
}

func handleNicksList(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	return map[string]interface{}{"nicks": s.server.deps.NickManager.GetNicks()}, nil
}

func handleOwnersList(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	return map[string]interface{}{"owners": s.server.deps.BotManager.GetOwners()}, nil
}
```

- [ ] **Step 4:** Register in `registerRoutes`:

```go
s.router.Register("bot.list", handleBotList)
s.router.Register("nicks.list", handleNicksList)
s.router.Register("owners.list", handleOwnersList)
```

- [ ] **Step 5:** Add to `testServer`: a `*config.Config` with a single bot:

```go
cfgFull := &config.Config{
	Bots: []config.BotConfig{{Server: "irc.example", Port: 6667, SSL: false, Vhost: "v"}},
	API:  cfg,
}
s := New(cfg, "testnode", Deps{
	Config:      cfgFull,
	BotManager:  &fakeBotManager{bots: []types.Bot{&fakeBot{nick: "a", connected: true, server: "irc.example", channels: []string{"#x"}}}},
	NickManager: &fakeNickManager{nicks: []string{"foo"}},
})
```

Also add `Config.Channels: []string{"#x"}` so `joinedChannels` sees something.

Add test:

```go
func TestBotList(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type": "request", "id": "2", "method": "bot.list"})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	r := resp["result"].(map[string]interface{})
	bots := r["bots"].([]interface{})
	if len(bots) != 1 {
		t.Fatalf("want 1 bot, got %d", len(bots))
	}
	b0 := bots[0].(map[string]interface{})
	if b0["current_nick"] != "a" {
		t.Fatalf("bad nick: %+v", b0)
	}
	if b0["bot_id"] == "" {
		t.Fatalf("empty bot_id")
	}
}
```

- [ ] **Step 6:** Run: `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 7:** `gofmt -w internal/api/`
- [ ] **Step 8:** Commit: `git add internal/api && git commit -m "api: implement bot.list, nicks.list, owners.list handlers"`

---

## Task 13: Single-bot control handlers (`bot.say`, `bot.join`, `bot.part`, `bot.quit`, `bot.reconnect`, `bot.change_nick`, `bot.raw`)

**Files:**
- Create: `internal/api/handlers_bot.go`
- Modify: `internal/api/server.go` (register)
- Modify: `internal/api/server_test.go` (add tests)

- [ ] **Step 1:** Create `internal/api/handlers_bot.go`:

```go
package api

import (
	"context"
	"strings"
)

type botIDParam struct {
	BotID string `json:"bot_id"`
}

type botSayParam struct {
	BotID   string `json:"bot_id"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

type botJoinParam struct {
	BotID   string `json:"bot_id"`
	Channel string `json:"channel"`
}

type botQuitParam struct {
	BotID  string `json:"bot_id"`
	Reason string `json:"reason"`
}

type botNickParam struct {
	BotID   string `json:"bot_id"`
	NewNick string `json:"new_nick"`
}

type botRawParam struct {
	BotID string `json:"bot_id"`
	Line  string `json:"line"`
}

func (s *Server) botOr404(id string) (interface{ types.Bot }, *HandlerError) {
	return nil, nil // placeholder — replaced by per-handler check
}

func handleBotSay(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botSayParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Target == "" || p.Message == "" {
		return nil, paramErr("target and message required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.SendMessage(p.Target, p.Message)
	return map[string]bool{"ok": true}, nil
}

func handleBotJoin(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botJoinParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.JoinChannel(p.Channel)
	return map[string]bool{"ok": true}, nil
}

func handleBotPart(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botJoinParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.PartChannel(p.Channel)
	return map[string]bool{"ok": true}, nil
}

func handleBotQuit(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botQuitParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	reason := p.Reason
	if reason == "" {
		reason = "API quit"
	}
	b.Quit(reason)
	return map[string]bool{"ok": true}, nil
}

func handleBotReconnect(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	go b.Reconnect()
	return map[string]bool{"ok": true}, nil
}

func handleBotChangeNick(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botNickParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.NewNick == "" {
		return nil, paramErr("new_nick required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.ChangeNick(p.NewNick)
	return map[string]bool{"ok": true}, nil
}

func handleBotRaw(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botRawParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Line == "" {
		return nil, paramErr("line required")
	}
	// Strip any CR/LF to prevent IRC command injection.
	line := strings.ReplaceAll(strings.ReplaceAll(p.Line, "\r", ""), "\n", "")
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.SendRaw(line)
	return map[string]bool{"ok": true}, nil
}
```

Remove the unused `botOr404` stub; add `"github.com/kofany/gNb/internal/types"` import if needed. Actually the placeholder is dead code, delete.

Final file drops `botOr404` entirely.

- [ ] **Step 2:** In `server.go` `registerRoutes`:

```go
s.router.Register("bot.say", handleBotSay)
s.router.Register("bot.join", handleBotJoin)
s.router.Register("bot.part", handleBotPart)
s.router.Register("bot.quit", handleBotQuit)
s.router.Register("bot.reconnect", handleBotReconnect)
s.router.Register("bot.change_nick", handleBotChangeNick)
s.router.Register("bot.raw", handleBotRaw)
```

- [ ] **Step 3:** Extend `fakeBot` to capture the calls:

```go
type fakeBot struct {
	// ...existing...
	sent      []string
	joined    []string
	parted    []string
	quit      string
	reconnect int
	nick      string
	newNick   string
	raw       []string
	mu        sync.Mutex
}

func (f *fakeBot) SendMessage(t, m string) {
	f.mu.Lock(); defer f.mu.Unlock()
	f.sent = append(f.sent, t+": "+m)
}
func (f *fakeBot) JoinChannel(c string) {
	f.mu.Lock(); defer f.mu.Unlock()
	f.joined = append(f.joined, c)
}
func (f *fakeBot) PartChannel(c string) {
	f.mu.Lock(); defer f.mu.Unlock()
	f.parted = append(f.parted, c)
}
func (f *fakeBot) Quit(m string)      { f.mu.Lock(); defer f.mu.Unlock(); f.quit = m }
func (f *fakeBot) Reconnect()         { f.mu.Lock(); defer f.mu.Unlock(); f.reconnect++ }
func (f *fakeBot) ChangeNick(n string){ f.mu.Lock(); defer f.mu.Unlock(); f.newNick = n }
func (f *fakeBot) SendRaw(l string)   { f.mu.Lock(); defer f.mu.Unlock(); f.raw = append(f.raw, l) }
```

Add `"sync"` import. Remove conflicting methods from earlier definition.

- [ ] **Step 4:** Add test per handler. Example for `bot.raw`:

```go
func TestBotRaw(t *testing.T) {
	_, ts := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.raw",
		"params": map[string]string{"bot_id": botID, "line": "WHO foo\r\nEXTRA"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	// Need access to the fakeBot from the test's BotManager
}
```

To make the assertion possible, retrieve the fake from the test fixture. Adjust `testServer` to return the fakeBotManager too:

```go
func testServer(t *testing.T) (*Server, *httptest.Server, *fakeBotManager) {
	...
	fbm := &fakeBotManager{bots: []types.Bot{fb}}
	...
	return s, ts, fbm
}
```

Update existing call sites (add `_` for the new return).

- [ ] **Step 5:** Add two more tests:

```go
func TestBotChangeNickPropagates(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.change_nick",
		"params": map[string]string{"bot_id": botID, "new_nick": "zz"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	fb := fbm.bots[0].(*fakeBot)
	fb.mu.Lock(); defer fb.mu.Unlock()
	if fb.newNick != "zz" {
		t.Fatalf("want newNick=zz, got %q", fb.newNick)
	}
}

func TestBotNotFound(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.raw",
		"params": map[string]string{"bot_id": "nonexistent", "line": "WHO"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "error" || resp["code"] != "not_found" {
		t.Fatalf("want not_found error, got %+v", resp)
	}
}
```

- [ ] **Step 6:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 7:** `gofmt -w internal/api/`
- [ ] **Step 8:** Commit: `git add internal/api && git commit -m "api: implement single-bot control handlers (say/join/part/quit/reconnect/change_nick/raw)"`

---

## Task 14: Node mass control (`node.mass_join`, `node.mass_part`, `node.mass_reconnect`, `node.mass_raw`, `node.mass_say`)

**Files:**
- Create: `internal/api/handlers_mass.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1:** Create `internal/api/handlers_mass.go`:

```go
package api

import (
	"context"
	"strings"
)

type massChanParam struct {
	Channel string `json:"channel"`
}

type massRawParam struct {
	Line string `json:"line"`
}

type massSayParam struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

func handleMassJoin(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massChanParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	bm := s.server.deps.BotManager
	if !bm.CanExecuteMassCommand("join") {
		return nil, &HandlerError{Code: ErrCooldown, Message: "mass-join cooldown active"}
	}
	bots := bm.GetBots()
	for _, b := range bots {
		b.JoinChannel(p.Channel)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassPart(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massChanParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	bm := s.server.deps.BotManager
	if !bm.CanExecuteMassCommand("part") {
		return nil, &HandlerError{Code: ErrCooldown, Message: "mass-part cooldown active"}
	}
	bots := bm.GetBots()
	for _, b := range bots {
		b.PartChannel(p.Channel)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassReconnect(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	bm := s.server.deps.BotManager
	if !bm.CanExecuteMassCommand("reconnect") {
		return nil, &HandlerError{Code: ErrCooldown, Message: "mass-reconnect cooldown active"}
	}
	bots := bm.GetBots()
	for _, b := range bots {
		go b.Reconnect()
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassRaw(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massRawParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Line == "" {
		return nil, paramErr("line required")
	}
	line := strings.ReplaceAll(strings.ReplaceAll(p.Line, "\r", ""), "\n", "")
	bots := s.server.deps.BotManager.GetBots()
	for _, b := range bots {
		b.SendRaw(line)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassSay(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massSayParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Target == "" || p.Message == "" {
		return nil, paramErr("target and message required")
	}
	bots := s.server.deps.BotManager.GetBots()
	for _, b := range bots {
		b.SendMessage(p.Target, p.Message)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}
```

- [ ] **Step 2:** Register:

```go
s.router.Register("node.mass_join", handleMassJoin)
s.router.Register("node.mass_part", handleMassPart)
s.router.Register("node.mass_reconnect", handleMassReconnect)
s.router.Register("node.mass_raw", handleMassRaw)
s.router.Register("node.mass_say", handleMassSay)
```

- [ ] **Step 3:** Add to `fakeBotManager`: a `canMass` map for testing cooldowns. For simplicity, default is `true`.

Add a test:

```go
func TestMassRawBroadcasts(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	// Add a 2nd bot
	fbm.bots = append(fbm.bots, &fakeBot{nick: "b", connected: true, server: "irc.example"})
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "node.mass_raw",
		"params": map[string]string{"line": "PING :x"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	r := resp["result"].(map[string]interface{})
	if int(r["affected"].(float64)) != 2 {
		t.Fatalf("want affected=2, got %v", r["affected"])
	}
	for _, b := range fbm.bots {
		fb := b.(*fakeBot)
		fb.mu.Lock()
		if len(fb.raw) != 1 || fb.raw[0] != "PING :x" {
			t.Errorf("bot raw not delivered: %+v", fb.raw)
		}
		fb.mu.Unlock()
	}
}

func TestMassJoinCooldown(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	fbm.massBlocked = map[string]bool{"join": true}
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "node.mass_join",
		"params": map[string]string{"channel": "#x"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "error" || resp["code"] != "cooldown" {
		t.Fatalf("want cooldown error, got %+v", resp)
	}
}
```

In `fakes_test.go` add field + method to fakeBotManager:

```go
type fakeBotManager struct {
	// ...existing...
	massBlocked map[string]bool
}

func (f *fakeBotManager) CanExecuteMassCommand(name string) bool {
	return !f.massBlocked[name]
}
```

Remove the old `CanExecuteMassCommand` that returns true.

- [ ] **Step 4:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 5:** `gofmt -w internal/api/`
- [ ] **Step 6:** Commit: `git add internal/api && git commit -m "api: implement node mass_{join,part,reconnect,raw,say} handlers"`

---

## Task 15: Admin handlers (`nicks.add/remove`, `owners.add/remove`, `bnc.start/stop`)

**Files:**
- Create: `internal/api/handlers_admin.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1:** Create `internal/api/handlers_admin.go`:

```go
package api

import "context"

type nickParam struct {
	Nick string `json:"nick"`
}

type ownerParam struct {
	Mask string `json:"mask"`
}

func handleNicksAdd(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p nickParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Nick == "" {
		return nil, paramErr("nick required")
	}
	if err := s.server.deps.NickManager.AddNick(p.Nick); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleNicksRemove(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p nickParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Nick == "" {
		return nil, paramErr("nick required")
	}
	if err := s.server.deps.NickManager.RemoveNick(p.Nick); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleOwnersAdd(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p ownerParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Mask == "" {
		return nil, paramErr("mask required")
	}
	if err := s.server.deps.BotManager.AddOwner(p.Mask); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleOwnersRemove(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p ownerParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Mask == "" {
		return nil, paramErr("mask required")
	}
	if err := s.server.deps.BotManager.RemoveOwner(p.Mask); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleBNCStart(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	port, password, err := b.StartBNC()
	if err != nil {
		return nil, internalErr(err)
	}
	return map[string]interface{}{
		"port":     port,
		"password": password,
	}, nil
}

func handleBNCStop(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.StopBNC()
	return map[string]bool{"ok": true}, nil
}
```

- [ ] **Step 2:** Register:

```go
s.router.Register("nicks.add", handleNicksAdd)
s.router.Register("nicks.remove", handleNicksRemove)
s.router.Register("owners.add", handleOwnersAdd)
s.router.Register("owners.remove", handleOwnersRemove)
s.router.Register("bnc.start", handleBNCStart)
s.router.Register("bnc.stop", handleBNCStop)
```

- [ ] **Step 3:** Add tests (sample):

```go
func TestNicksAdd(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "nicks.add",
		"params": map[string]string{"nick": "newnick"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
}
```

- [ ] **Step 4:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 5:** `gofmt -w internal/api/`
- [ ] **Step 6:** Commit: `git add internal/api && git commit -m "api: implement nicks/owners admin handlers and bnc.start/stop"`

---

## Task 16: `events.subscribe` + `events.unsubscribe` with replay

**Files:**
- Create: `internal/api/handlers_events.go`
- Modify: `internal/api/session.go` (sub pump goroutine)
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1:** Create `internal/api/handlers_events.go`:

```go
package api

import "context"

type subscribeParam struct {
	Topics     []string `json:"topics"`
	ReplayLast int      `json:"replay_last"`
}

func handleEventsSubscribe(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p subscribeParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if s.sub != nil {
		s.server.hub.Unsubscribe(s.sub)
		s.sub = nil
	}
	sub := s.server.hub.Subscribe(p.Topics, 256)
	s.sub = sub
	s.startSubPump(sub)

	replayed := s.server.hub.Replay(sub, p.ReplayLast)
	for _, m := range replayed {
		s.send(m)
	}

	return map[string]interface{}{
		"cursor":   s.server.hub.Seq(),
		"replayed": len(replayed),
	}, nil
}

func handleEventsUnsubscribe(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	if s.sub != nil {
		s.server.hub.Unsubscribe(s.sub)
		s.sub = nil
	}
	return map[string]bool{"ok": true}, nil
}
```

- [ ] **Step 2:** In `session.go`, add `startSubPump`:

```go
func (s *Session) startSubPump(sub *Subscriber) {
	go func() {
		for msg := range sub.Ch() {
			s.send(msg)
		}
	}()
}
```

- [ ] **Step 3:** Register handlers + add test:

```go
s.router.Register("events.subscribe", handleEventsSubscribe)
s.router.Register("events.unsubscribe", handleEventsUnsubscribe)
```

Test:

```go
func TestEventsSubscribeAndReceive(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "events.subscribe",
		"params": map[string]interface{}{"topics": []string{"bot.connected"}, "replay_last": 0},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	// Publish an event from the hub; the session should receive it.
	srv.hub.Publish("bot.connected", map[string]string{"bot_id": "x"})
	var ev map[string]interface{}
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	_ = wsjson.Read(rctx2, c, &ev)
	if ev["type"] != "event" || ev["event"] != "bot.connected" {
		t.Fatalf("bad event: %+v", ev)
	}
}

func TestEventsReplay(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	// Pre-publish events before subscribe.
	for i := 0; i < 3; i++ {
		srv.hub.Publish("bot.connected", map[string]int{"i": i})
	}
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "events.subscribe",
		"params": map[string]interface{}{"replay_last": 2},
	})
	// Expect 1 response + 2 replay events.
	seen := map[string]int{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		var m map[string]interface{}
		if err := wsjson.Read(rctx, c, &m); err != nil {
			t.Fatal(err)
		}
		seen[m["type"].(string)]++
	}
	if seen["response"] != 1 || seen["event"] != 2 {
		t.Fatalf("bad breakdown: %+v", seen)
	}
}
```

- [ ] **Step 4:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 5:** `gofmt -w internal/api/`
- [ ] **Step 6:** Commit: `git add internal/api && git commit -m "api: implement events.subscribe/unsubscribe with ring-buffer replay"`

---

## Task 17: `bot.attach` / `bot.detach` + attach event routing

**Files:**
- Create: `internal/api/handlers_attach.go`
- Modify: `internal/api/attach.go` (add `Publish` method)
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1:** Extend `attach.go` with publish helper that delivers to all attached sessions:

```go
// Publish finds sessions attached to bot_id and delivers msg to each session's send method.
func (am *AttachManager) Publish(srv *Server, botID string, msg EventMsg) {
	am.mu.RLock()
	set, ok := am.subs[botID]
	if !ok {
		am.mu.RUnlock()
		return
	}
	ids := make([]uint64, 0, len(set))
	for sid := range set {
		ids = append(ids, sid)
	}
	am.mu.RUnlock()

	srv.sessMu.Lock()
	sessions := make([]*Session, 0, len(ids))
	for _, sid := range ids {
		if sess, ok := srv.sessions[sid]; ok {
			sessions = append(sessions, sess)
		}
	}
	srv.sessMu.Unlock()
	for _, sess := range sessions {
		sess.send(msg)
	}
}

// NewAttachEvent creates an attach-scoped EventMsg with the standard shape.
func (srv *Server) NewAttachEvent(event string, data interface{}) EventMsg {
	return NewEvent(srv.nodeID, event, srv.hub.seq.Add(1), nowUTC(), data)
}
```

Add:

```go
func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }
```

And import `"time"`.

- [ ] **Step 2:** Create `internal/api/handlers_attach.go`:

```go
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

func newAttachID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func handleBotAttach(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if s.server.BotByID(p.BotID) == nil {
		return nil, notFound("bot not found")
	}
	s.server.attach.Attach(p.BotID, s.id)
	s.trackAttach(p.BotID)
	return map[string]string{"attach_id": newAttachID()}, nil
}

func handleBotDetach(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	s.server.attach.Detach(p.BotID, s.id)
	s.untrackAttach(p.BotID)
	return map[string]bool{"ok": true}, nil
}
```

- [ ] **Step 3:** Register:

```go
s.router.Register("bot.attach", handleBotAttach)
s.router.Register("bot.detach", handleBotDetach)
```

- [ ] **Step 4:** Test:

```go
func TestBotAttachReceivesEvent(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.attach",
		"params": map[string]string{"bot_id": botID},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	// Simulate attach event
	srv.attach.Publish(srv, botID, srv.NewAttachEvent("bot.attach.privmsg", map[string]string{"target": "#c", "text": "hi"}))
	var ev map[string]interface{}
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	_ = wsjson.Read(rctx2, c, &ev)
	if ev["event"] != "bot.attach.privmsg" {
		t.Fatalf("bad event: %+v", ev)
	}
}
```

- [ ] **Step 5:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 6:** `gofmt -w internal/api/`
- [ ] **Step 7:** Commit: `git add internal/api && git commit -m "api: implement bot.attach/bot.detach with per-bot event fan-out"`

---

## Task 18: `EventSink` impl inside API server

**Files:**
- Create: `internal/api/sink.go`
- Create: `internal/api/sink_test.go`
- Modify: `internal/api/server.go` (expose `Sink() types.EventSink`)

- [ ] **Step 1:** Create `internal/api/sink.go`:

```go
package api

import (
	"github.com/kofany/gNb/internal/types"
	irc "github.com/kofany/go-ircevo"
)

// serverSink adapts Server into types.EventSink. It emits to the hub for
// lifecycle events and to the AttachManager for IRC firehose.
type serverSink struct{ srv *Server }

// Sink returns the types.EventSink that Bot/BotManager wire to.
func (s *Server) Sink() types.EventSink { return &serverSink{srv: s} }

func (s *serverSink) BotConnected(botID, nick, server string) {
	s.srv.hub.Publish("bot.connected", map[string]interface{}{"bot_id": botID, "nick": nick, "server": server})
}
func (s *serverSink) BotDisconnected(botID, reason string) {
	s.srv.hub.Publish("bot.disconnected", map[string]interface{}{"bot_id": botID, "reason": reason})
}
func (s *serverSink) BotNickChanged(botID, oldNick, newNick string) {
	s.srv.hub.Publish("bot.nick_changed", map[string]interface{}{"bot_id": botID, "old": oldNick, "new": newNick})
}
func (s *serverSink) BotNickCaptured(botID, nick, kind string) {
	s.srv.hub.Publish("bot.nick_captured", map[string]interface{}{"bot_id": botID, "nick": nick, "kind": kind})
}
func (s *serverSink) BotJoinedChannel(botID, channel string) {
	s.srv.hub.Publish("bot.joined_channel", map[string]interface{}{"bot_id": botID, "channel": channel})
}
func (s *serverSink) BotPartedChannel(botID, channel string) {
	s.srv.hub.Publish("bot.parted_channel", map[string]interface{}{"bot_id": botID, "channel": channel})
}
func (s *serverSink) BotKicked(botID, channel, by, reason string) {
	s.srv.hub.Publish("bot.kicked", map[string]interface{}{"bot_id": botID, "channel": channel, "by": by, "reason": reason})
}
func (s *serverSink) BotBanned(botID string, code int) {
	s.srv.hub.Publish("bot.banned_from_server", map[string]interface{}{"bot_id": botID, "code": code})
}
func (s *serverSink) BotAdded(botID, server string, port int, ssl bool, vhost string) {
	s.srv.hub.Publish("node.bot_added", map[string]interface{}{"bot_id": botID, "config": map[string]interface{}{"server": server, "port": port, "ssl": ssl, "vhost": vhost}})
}
func (s *serverSink) BotRemoved(botID string) {
	s.srv.hub.Publish("node.bot_removed", map[string]interface{}{"bot_id": botID})
}
func (s *serverSink) NicksChanged(nicks []string) {
	s.srv.hub.Publish("nicks.changed", map[string]interface{}{"nicks": nicks})
}
func (s *serverSink) OwnersChanged(owners []string) {
	s.srv.hub.Publish("owners.changed", map[string]interface{}{"owners": owners})
}

func (s *serverSink) BotIRCEvent(botID string, e *irc.Event) {
	msg := translateIRCEvent(s.srv, botID, e)
	if msg == nil {
		return
	}
	s.srv.attach.Publish(s.srv, botID, *msg)
}
```

- [ ] **Step 2:** Add `internal/api/sink_translate.go` with `translateIRCEvent`:

```go
package api

import (
	irc "github.com/kofany/go-ircevo"
)

// translateIRCEvent converts an irc.Event into a bot.attach.* EventMsg.
// Returns nil for events we don't care to stream (numerics, etc. except when raw_in).
// It always emits raw_in for every inbound event.
func translateIRCEvent(srv *Server, botID string, e *irc.Event) *EventMsg {
	// Always stream raw_in for attached sessions — one event per incoming line.
	rawMsg := srv.NewAttachEvent("bot.attach.raw_in", map[string]interface{}{
		"bot_id": botID,
		"line":   e.Raw,
	})
	// Emit raw_in directly — caller delivers to subscribers.
	// We also emit a higher-level event based on Code.
	base := map[string]interface{}{
		"bot_id": botID,
		"from":   map[string]string{"nick": e.Nick, "user": e.User, "host": e.Host},
	}
	switch e.Code {
	case "PRIVMSG":
		if len(e.Arguments) > 0 {
			base["target"] = e.Arguments[0]
		}
		base["text"] = e.Message()
		hl := srv.NewAttachEvent("bot.attach.privmsg", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "NOTICE":
		if len(e.Arguments) > 0 {
			base["target"] = e.Arguments[0]
		}
		base["text"] = e.Message()
		hl := srv.NewAttachEvent("bot.attach.notice", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "JOIN":
		ch := ""
		if len(e.Arguments) > 0 {
			ch = e.Arguments[0]
		}
		base["channel"] = ch
		delete(base, "from")
		base["who"] = map[string]string{"nick": e.Nick, "user": e.User, "host": e.Host}
		hl := srv.NewAttachEvent("bot.attach.join", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "PART":
		ch := ""
		if len(e.Arguments) > 0 {
			ch = e.Arguments[0]
		}
		base["channel"] = ch
		base["reason"] = e.Message()
		delete(base, "from")
		base["who"] = map[string]string{"nick": e.Nick, "user": e.User, "host": e.Host}
		hl := srv.NewAttachEvent("bot.attach.part", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "QUIT":
		base["reason"] = e.Message()
		delete(base, "from")
		base["who"] = map[string]string{"nick": e.Nick, "user": e.User, "host": e.Host}
		hl := srv.NewAttachEvent("bot.attach.quit", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "KICK":
		channel := ""
		target := ""
		if len(e.Arguments) >= 2 {
			channel = e.Arguments[0]
			target = e.Arguments[1]
		}
		base["channel"] = channel
		base["target"] = target
		base["reason"] = e.Message()
		base["by"] = map[string]string{"nick": e.Nick, "user": e.User, "host": e.Host}
		delete(base, "from")
		hl := srv.NewAttachEvent("bot.attach.kick", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "MODE":
		target := ""
		args := []string{}
		if len(e.Arguments) >= 1 {
			target = e.Arguments[0]
		}
		if len(e.Arguments) >= 2 {
			args = e.Arguments[1:]
		}
		base["target"] = target
		base["args"] = args
		hl := srv.NewAttachEvent("bot.attach.mode", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "TOPIC":
		ch := ""
		if len(e.Arguments) > 0 {
			ch = e.Arguments[0]
		}
		base["channel"] = ch
		base["topic"] = e.Message()
		hl := srv.NewAttachEvent("bot.attach.topic", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "NICK":
		base["new_nick"] = e.Message()
		hl := srv.NewAttachEvent("bot.attach.nick", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	case "CTCP", "CTCP_ACTION":
		base["command"] = e.Code
		base["text"] = e.Message()
		hl := srv.NewAttachEvent("bot.attach.ctcp", base)
		srv.attach.Publish(srv, botID, rawMsg)
		return &hl
	}
	// No high-level translation: still emit raw_in only.
	srv.attach.Publish(srv, botID, rawMsg)
	return nil
}
```

Note: we deliver rawMsg via `srv.attach.Publish` directly and return only the high-level message (which the caller will also publish). That duplicates Publish — simplify by publishing both inline and returning nil always:

```go
func translateIRCEvent(srv *Server, botID string, e *irc.Event) *EventMsg {
	srv.attach.Publish(srv, botID, srv.NewAttachEvent("bot.attach.raw_in", map[string]interface{}{"bot_id": botID, "line": e.Raw}))
	hl := highLevelAttachEvent(srv, botID, e)
	if hl != nil {
		srv.attach.Publish(srv, botID, *hl)
	}
	return nil
}
```

And refactor: `highLevelAttachEvent` returns `*EventMsg` from the switch. The caller `BotIRCEvent` no longer publishes the return value (since translateIRCEvent already did).

Revise BotIRCEvent:

```go
func (s *serverSink) BotIRCEvent(botID string, e *irc.Event) {
	translateIRCEvent(s.srv, botID, e)
}
```

And adapt the switch to return only the high-level EventMsg (raw_in published outside the switch):

```go
func highLevelAttachEvent(srv *Server, botID string, e *irc.Event) *EventMsg {
	// same switch body as above, each case constructs `base` and returns &EventMsg
	// BUT: drop all srv.attach.Publish(rawMsg) calls — those are already done
}
```

**Final cleaner version:** drop `translateIRCEvent` and have `BotIRCEvent` do the two publishes directly:

```go
func (s *serverSink) BotIRCEvent(botID string, e *irc.Event) {
	srv := s.srv
	srv.attach.Publish(srv, botID, srv.NewAttachEvent("bot.attach.raw_in", map[string]interface{}{"bot_id": botID, "line": e.Raw}))
	if hl := translateIRCEventToHighLevel(srv, botID, e); hl != nil {
		srv.attach.Publish(srv, botID, *hl)
	}
}

func translateIRCEventToHighLevel(srv *Server, botID string, e *irc.Event) *EventMsg {
	// switch as above, returns *EventMsg or nil
}
```

Use this shape in the actual code you write.

- [ ] **Step 3:** Test that the sink pushes correctly:

```go
// sink_test.go
package api

import (
	"testing"
)

func TestSinkPublishesLifecycleEvents(t *testing.T) {
	srv, _, _ := testServer(t)
	sub := srv.hub.Subscribe(nil, 8)
	defer srv.hub.Unsubscribe(sub)

	sink := srv.Sink()
	sink.BotConnected("b1", "bot1", "irc.x")
	msg := <-sub.Ch()
	if msg.Event != "bot.connected" {
		t.Fatalf("bad: %s", msg.Event)
	}

	sink.BotNickChanged("b1", "old", "new")
	msg = <-sub.Ch()
	if msg.Event != "bot.nick_changed" {
		t.Fatalf("bad: %s", msg.Event)
	}
	d := msg.Data.(map[string]interface{})
	if d["old"] != "old" || d["new"] != "new" {
		t.Fatalf("bad payload: %+v", d)
	}
}
```

- [ ] **Step 4:** `go test -race -timeout 30s ./internal/api/... -v`
- [ ] **Step 5:** `gofmt -w internal/api/`
- [ ] **Step 6:** Commit: `git add internal/api && git commit -m "api: add serverSink implementing types.EventSink (lifecycle + IRC firehose)"`

---

## Task 19: Plumb EventSink through BotManager/Bot

**Files:**
- Modify: `internal/types/interfaces.go` — add `SetEventSink` to `BotManager` interface
- Modify: `internal/bot/manager.go` — add field + setter, distribute to bots, call in mutators
- Modify: `internal/bot/bot.go` — add field + setter, hook into callbacks
- Modify: `internal/nickmanager/nickmanager.go` — call sink on nicks.add/remove
- Modify: `internal/bot/manager.go` — call sink on owners.add/remove, bot_added/removed

- [ ] **Step 1:** In `internal/types/interfaces.go`, add to `BotManager` interface:

```go
SetEventSink(sink EventSink)
```

And to `Bot` interface:

```go
SetEventSink(sink EventSink)
GetBotID() string
```

(We need `GetBotID` so that Bot can emit events with its own ID without BotManager lookup.)

And `NickManager`:

```go
SetEventSink(sink EventSink)
```

- [ ] **Step 2:** In `internal/bot/bot.go`:
  - Add field to `Bot` struct: `sink types.EventSink` and `botID string`.
  - Add `func (b *Bot) SetEventSink(sink types.EventSink) { b.mutex.Lock(); b.sink = sink; b.mutex.Unlock() }`.
  - Add `func (b *Bot) GetBotID() string { return b.botID }`.
  - Add private helper `func (b *Bot) currentSink() types.EventSink { b.mutex.Lock(); s := b.sink; b.mutex.Unlock(); return s }`.

- [ ] **Step 3:** In `NewBot`, accept a new `botID string` param and set `b.botID = botID`. Propagate through `BotManager.NewBotManager` which already creates bots in a loop — it passes the index; compute `botID` via `api.ComputeBotID`. But to avoid a cycle `bot` ← `api`, move `ComputeBotID` to a shared package or have manager pass the computed id.

Cycle-safe approach: leave ComputeBotID in `internal/api` (good), and have `BotManager` compute it at bot creation time by calling an injected function or replicating the hash. To keep cleanly acyclic, duplicate the tiny one-liner hash into `internal/bot` in a new private file `internal/bot/bot_id.go`:

```go
package bot

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

func computeBotID(server string, port int, vhost string, index int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%s:%d", server, port, vhost, index)))
	return hex.EncodeToString(sum[:])[:12]
}
```

Add a `TestComputeBotIDMatchesAPI` guard test in `internal/bot/bot_id_test.go` to ensure it stays in sync:

```go
package bot

import "testing"

func TestComputeBotIDShape(t *testing.T) {
	id := computeBotID("s", 1, "v", 0)
	if len(id) != 12 {
		t.Fatalf("want 12, got %d", len(id))
	}
}
```

(The real sync test goes in `internal/api/` against bot's computation — but keeping them isolated with the same formula is fine; we'll add a cross-package test in Task 27.)

- [ ] **Step 4:** In `internal/bot/manager.go` `NewBotManager`, set `botID = computeBotID(...)` per bot and pass to `NewBot`.

- [ ] **Step 5:** Add `SetEventSink` on BotManager that fans out to all bots + saves its own reference:

```go
func (bm *BotManager) SetEventSink(sink types.EventSink) {
	bm.mutex.Lock()
	bm.sink = sink
	bots := append([]types.Bot(nil), bm.bots...)
	bm.mutex.Unlock()
	for _, b := range bots {
		b.SetEventSink(sink)
	}
	if bm.nickManager != nil {
		bm.nickManager.SetEventSink(sink)
	}
}
```

Add field `sink types.EventSink` to `BotManager` struct.

- [ ] **Step 6:** In `NickManager`:
  - Field `sink types.EventSink`.
  - `func (nm *NickManager) SetEventSink(s types.EventSink) { nm.mutex.Lock(); nm.sink = s; nm.mutex.Unlock() }`.
  - After successful AddNick/RemoveNick, emit `nm.sink.NicksChanged(nm.GetNicks())` (outside locks).

- [ ] **Step 7:** In BotManager:
  - After successful `AddOwner` / `RemoveOwner`, emit `bm.sink.OwnersChanged(bm.GetOwners())`.
  - In `cleanupDisconnectedBots` or `RemoveBotFromManager`, after removal emit `bm.sink.BotRemoved(bot.GetBotID())`.
  - After `StartBots` (or inside the loop when adding a bot initially), emit `bm.sink.BotAdded(...)` for each bot.

- [ ] **Step 8:** Build: `go build ./...` — must compile. Run existing tests: `go test ./... -count=1` — must pass (no regression).
- [ ] **Step 9:** `gofmt -w .`
- [ ] **Step 10:** Commit: `git add -A && git commit -m "api: plumb EventSink through BotManager, Bot, NickManager"`

---

## Task 20: Bot emits lifecycle events (connected/disconnected/nick_changed/nick_captured)

**Files:**
- Modify: `internal/bot/bot.go` (callbacks 001/NICK/303/432/437/DISCONNECTED)
- Modify: `internal/bot/manager.go`

- [ ] **Step 1:** In the `001` callback (welcome → connected), after existing logic:

```go
if sink := b.currentSink(); sink != nil {
	sink.BotConnected(b.botID, b.GetCurrentNick(), b.GetServerName())
}
```

- [ ] **Step 2:** In the `DISCONNECTED` callback:

```go
if sink := b.currentSink(); sink != nil {
	sink.BotDisconnected(b.botID, "")
}
```

- [ ] **Step 3:** In `NICK` callback (self nick change), determine old vs new; after update emit:

```go
sink.BotNickChanged(b.botID, oldNick, newNick)
```

And if newNick length == 1 (single-letter capture), also:

```go
sink.BotNickCaptured(b.botID, newNick, "letter")
```

If newNick is in `nickManager.GetNicks()` (priority list), emit:

```go
sink.BotNickCaptured(b.botID, newNick, "priority")
```

- [ ] **Step 4:** For 465/466 (banned): after existing logic in the handler:

```go
sink.BotBanned(b.botID, code)
```

- [ ] **Step 5:** Run `go test ./... -count=1` — existing tests must still pass.
- [ ] **Step 6:** `gofmt -w .`
- [ ] **Step 7:** Commit: `git add internal/bot && git commit -m "api: emit bot connected/disconnected/nick_changed/nick_captured/banned events"`

---

## Task 21: Bot emits channel lifecycle events

**Files:**
- Modify: `internal/bot/bot.go` (JOIN/PART/KICK callbacks)

- [ ] **Step 1:** In the `JOIN` callback, if `e.Nick == b.GetCurrentNick()`, emit:

```go
sink.BotJoinedChannel(b.botID, e.Arguments[0])
```

- [ ] **Step 2:** In `PART`: same, if self, emit `BotPartedChannel`.

- [ ] **Step 3:** In `KICK`: if `len(e.Arguments) >= 2 && e.Arguments[1] == b.GetCurrentNick()`, emit:

```go
sink.BotKicked(b.botID, e.Arguments[0], e.Nick, e.Message())
```

- [ ] **Step 4:** `go test ./... -count=1` — pass.
- [ ] **Step 5:** `gofmt -w .`
- [ ] **Step 6:** Commit: `git add internal/bot/bot.go && git commit -m "api: emit bot join/part/kick self-events"`

---

## Task 22: IRC firehose for attach

**Files:**
- Modify: `internal/bot/bot.go` — in `addCallbacks`, register a wildcard callback that forwards to `sink.BotIRCEvent`.

- [ ] **Step 1:** In `addCallbacks` (or dedicated setup), add:

```go
b.Connection.AddCallback("*", func(e *irc.Event) {
	if sink := b.currentSink(); sink != nil {
		sink.BotIRCEvent(b.botID, e)
	}
})
```

(If a "*" callback already exists with other logic, chain them — append to the same function.)

- [ ] **Step 2:** Run tests: `go test ./... -count=1`.
- [ ] **Step 3:** `gofmt -w internal/bot/`
- [ ] **Step 4:** Commit: `git add internal/bot/bot.go && git commit -m "api: forward every IRC event to EventSink for attach routing"`

---

## Task 23: Wire server startup in `cmd/main.go`

**Files:**
- Modify: `cmd/main.go`

- [ ] **Step 1:** After `go botManager.StartBots()` and `nm.Start()`, add:

```go
if cfg.API.Enabled {
	nodeID, err := api.LoadOrCreateNodeID(api.DefaultNodeIDPath())
	if err != nil {
		util.Error("API: failed to load/create node_id: %v", err)
	} else {
		apiSrv := api.New(cfg.API, nodeID, api.Deps{
			Config:      cfg,
			BotManager:  botManager,
			NickManager: nm,
		})
		botManager.SetEventSink(apiSrv.Sink())
		apiCtx, cancelAPI := context.WithCancel(context.Background())
		go func() {
			if err := apiSrv.Run(apiCtx); err != nil {
				util.Error("API: %v", err)
			}
		}()
		defer cancelAPI()
	}
}
```

Add imports for `context`, `github.com/kofany/gNb/internal/api`.

- [ ] **Step 2:** Build: `go build -o gNb ./cmd/main.go` (or `make`).
- [ ] **Step 3:** `go vet ./...`
- [ ] **Step 4:** `gofmt -w cmd/`
- [ ] **Step 5:** Commit: `git add cmd/main.go && git commit -m "api: bootstrap API server in main with graceful shutdown"`

---

## Task 24: Integration smoke — E2E test that publishes events

**Files:**
- Create: `internal/api/integration_test.go`

- [ ] **Step 1:** Create:

```go
package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestEndToEndLifecycleFlow(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()

	// subscribe to all events
	if err := wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "sub", "method": "events.subscribe",
		"params": map[string]interface{}{"replay_last": 0},
	}); err != nil {
		t.Fatal(err)
	}
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("sub failed: %+v", resp)
	}

	// Drive sink events from the server side
	sink := srv.Sink()
	sink.BotConnected("abc", "bot1", "irc.example")
	sink.BotNickChanged("abc", "bot1", "a")
	sink.BotNickCaptured("abc", "a", "letter")

	// Read 3 events
	want := []string{"bot.connected", "bot.nick_changed", "bot.nick_captured"}
	for _, ev := range want {
		var m map[string]interface{}
		rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
		err := wsjson.Read(rctx2, c, &m)
		cancel2()
		if err != nil {
			t.Fatal(err)
		}
		if m["event"] != ev {
			t.Fatalf("want %s, got %v", ev, m["event"])
		}
	}

	// Confirm attach gate: attach not called yet, so a BotIRCEvent should NOT deliver.
	// Trigger a fake irc event via sink — but since we'd need a real irc.Event, skip here.
	_ = strings.Contains // silence unused import if applicable
}
```

- [ ] **Step 2:** `go test -race -timeout 60s ./internal/api/... -v`
- [ ] **Step 3:** `gofmt -w internal/api/`
- [ ] **Step 4:** Commit: `git add internal/api/integration_test.go && git commit -m "api: add end-to-end lifecycle event flow integration test"`

---

## Task 25: Update `configs/config.example.yaml`

**Files:**
- Modify: `configs/config.example.yaml`

- [ ] **Step 1:** Append:

```yaml

# Panel API (WebSocket). Plain HTTP by default — expose via cloudflared for wss://.
# Set api.enabled: true to turn on. Generate a fresh 64-char hex token with:
#   python -c 'import secrets; print(secrets.token_hex(32))'
api:
  enabled: false
  node_name: "my-node"
  bind_addr: "127.0.0.1:7766"
  auth_token: ""
  tls_cert_file: ""
  tls_key_file: ""
  event_buffer: 1000
  max_connections: 4
```

- [ ] **Step 2:** Commit: `git add configs/config.example.yaml && git commit -m "api: document api section in config.example.yaml"`

---

## Task 26: Full verification sweep

**Files:** none.

- [ ] **Step 1:** `gofmt -l $(git ls-files '*.go')` — must be empty.
- [ ] **Step 2:** `go vet ./...` — no warnings.
- [ ] **Step 3:** `go build ./...` — clean.
- [ ] **Step 4:** `go test -race -timeout 120s ./...` — green.
- [ ] **Step 5:** Smoke: build the binary `make` (or `go build -o gNb ./cmd/main.go`). With a minimal `configs/config.yaml` that sets `api.enabled: true` and a valid token + a dummy bots entry pointing at 127.0.0.1:65535 (unreachable — bot will fail to connect), run `./gNb -dev &` and use `websocat` or the small Go smoke client below to verify:
  - auth.login returns response
  - events.subscribe returns cursor
  - eventually bot.disconnected (or bot.banned) event fires or the bot cleanup event appears

Smoke client (ad-hoc — no commit, just a scratch verification):
```bash
cat <<'EOF' > /tmp/smoke_client.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func main() {
	url := os.Args[1]
	token := os.Args[2]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil { panic(err) }
	defer c.Close(websocket.StatusNormalClosure, "")
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type":"request","id":"1","method":"auth.login","params":map[string]string{"token":token}})
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type":"request","id":"2","method":"events.subscribe","params":map[string]interface{}{"replay_last":100}})
	for {
		var m map[string]interface{}
		if err := wsjson.Read(ctx, c, &m); err != nil { fmt.Println("err:", err); return }
		fmt.Printf("<- %+v\n", m)
	}
}
EOF
```

Build and run: `go run /tmp/smoke_client.go ws://127.0.0.1:7766/ws <token>`.

- [ ] **Step 6:** If smoke passes, nothing to commit (no repo changes) — move on. If smoke reveals a bug, fix, commit, and re-verify before declaring done.

---

## Task 27: Double-review pass

**Files:** none (or fix-ups as needed).

- [ ] **Step 1:** Use the code-reviewer subagent on the full branch diff. Command from shell:

```bash
git log --oneline origin/main..HEAD | wc -l   # verify commit count
git diff origin/main..HEAD --stat            # confirm scope
```

Then dispatch `superpowers:code-reviewer` subagent with the branch diff and ask for a correctness + quality review. Incorporate fixes (new commits, don't amend).

- [ ] **Step 2:** Manual spec coverage checklist: open `docs/superpowers/specs/2026-04-24-api-websocket-design.md` side-by-side with the code. Tick every method in §7, every event in §8. Any gap → add a task, implement, commit.

- [ ] **Step 3:** Re-run full verification sweep from Task 26, steps 1-4.

- [ ] **Step 4:** If `git status` is clean and the sweep is green, this plan is done. Otherwise commit the last fix and re-check.
