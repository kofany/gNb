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

	subMu     sync.RWMutex
	subs      map[uint64]*Subscriber
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
