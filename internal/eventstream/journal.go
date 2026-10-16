// Package eventstream provides a small, bounded replay journal for transient
// event streams. It is transport-agnostic and intentionally not a task store.
package eventstream

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultEventsPerStream = 256
	defaultMaxStreams      = 1024
	maxEventBytes          = 64 << 10
	maxBytesPerStream      = 512 << 10
)

// Record is one immutable event retained by a Journal.
type Record struct {
	ID        string
	Type      string
	Data      []byte
	CreatedAt time.Time
	sequence  uint64
}

type stream struct {
	next    uint64
	records []Record
	bytes   int
	touched uint64
}

// Replay describes the records retained after a client cursor. Gap means the
// cursor predates the bounded buffer. Reset means it belongs to another server
// generation (for example after restart or reconnecting through another Pod).
type Replay struct {
	Records  []Record
	Gap      bool
	Reset    bool
	OldestID string
	LatestID string
}

// Journal retains a bounded number of events for a bounded number of streams.
// It supports quick reconnects but does not replace durable task snapshots.
type Journal struct {
	mu              sync.Mutex
	generation      string
	eventsPerStream int
	maxStreams      int
	touchSequence   uint64
	streams         map[string]*stream
}

// NewJournal creates a replay journal. Non-positive limits use conservative
// defaults suitable for application event streams.
func NewJournal(eventsPerStream, maxStreams int) *Journal {
	if eventsPerStream <= 0 {
		eventsPerStream = defaultEventsPerStream
	}
	if maxStreams <= 0 {
		maxStreams = defaultMaxStreams
	}
	return &Journal{
		generation:      newGeneration(),
		eventsPerStream: eventsPerStream,
		maxStreams:      maxStreams,
		streams:         make(map[string]*stream),
	}
}

// Append stores one event and returns its generated cursor.
func (journal *Journal) Append(key, eventType string, data []byte) Record {
	if journal == nil || strings.TrimSpace(key) == "" {
		return Record{Type: eventType, Data: append([]byte(nil), data...), CreatedAt: time.Now().UTC()}
	}
	if len(data) > maxEventBytes {
		// Oversized payloads remain live-only. Retaining them would let a small
		// number of verbose events consume the entire reconnect budget.
		return Record{Type: eventType, CreatedAt: time.Now().UTC()}
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()

	key = strings.TrimSpace(key)
	now := time.Now().UTC()
	current := journal.streams[key]
	if current == nil {
		journal.pruneOldestLocked()
		current = &stream{}
		journal.streams[key] = current
	}
	current.next++
	journal.touchLocked(current)
	record := Record{
		ID:        journal.eventID(key, current.next),
		Type:      strings.TrimSpace(eventType),
		Data:      append([]byte(nil), data...),
		CreatedAt: now,
		sequence:  current.next,
	}
	current.records = append(current.records, record)
	current.bytes += len(record.Data)
	for len(current.records) > journal.eventsPerStream || current.bytes > maxBytesPerStream {
		current.bytes -= len(current.records[0].Data)
		current.records = current.records[1:]
	}
	current.records = append([]Record(nil), current.records...)
	return cloneRecord(record)
}

// After returns events newer than lastEventID. An empty cursor intentionally
// returns no history, so a first-time subscriber begins with live events and a
// task snapshot instead of receiving an arbitrary old transcript.
func (journal *Journal) After(key, lastEventID string) Replay {
	if journal == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(lastEventID) == "" {
		return Replay{}
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()

	generation, streamID, sequence, ok := parseEventID(lastEventID)
	if !ok || generation != journal.generation || streamID != streamToken(strings.TrimSpace(key)) {
		return Replay{Reset: true}
	}
	current := journal.streams[strings.TrimSpace(key)]
	if current == nil || len(current.records) == 0 {
		return Replay{Reset: true}
	}
	if sequence > current.next {
		return Replay{Reset: true, OldestID: current.records[0].ID, LatestID: current.records[len(current.records)-1].ID}
	}
	journal.touchLocked(current)
	replay := Replay{
		OldestID: current.records[0].ID,
		LatestID: current.records[len(current.records)-1].ID,
	}
	if sequence+1 < current.records[0].sequence {
		replay.Gap = true
	}
	for _, record := range current.records {
		if record.sequence > sequence {
			replay.Records = append(replay.Records, cloneRecord(record))
		}
	}
	return replay
}

func (journal *Journal) eventID(key string, sequence uint64) string {
	return journal.generation + ":" + streamToken(key) + ":" + strconv.FormatUint(sequence, 10)
}

func (journal *Journal) pruneOldestLocked() {
	if len(journal.streams) < journal.maxStreams {
		return
	}
	oldestKey := ""
	var oldestTouch uint64
	for key, candidate := range journal.streams {
		if oldestKey == "" || candidate.touched < oldestTouch {
			oldestKey = key
			oldestTouch = candidate.touched
		}
	}
	if oldestKey != "" {
		delete(journal.streams, oldestKey)
	}
}

func (journal *Journal) touchLocked(current *stream) {
	journal.touchSequence++
	current.touched = journal.touchSequence
}

func cloneRecord(record Record) Record {
	record.Data = append([]byte(nil), record.Data...)
	return record
}

func parseEventID(value string) (string, string, uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", 0, false
	}
	sequence, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return "", "", 0, false
	}
	return parts[0], parts[1], sequence, true
}

func streamToken(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:8])
}

func newGeneration() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
