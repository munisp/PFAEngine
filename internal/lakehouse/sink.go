package lakehouse

import "sync"

// MemorySink is a slice-backed, thread-safe Sink for tests and local dev.
type MemorySink struct {
	mu     sync.Mutex
	events []Event
}

// NewMemorySink returns an empty in-memory sink.
func NewMemorySink() *MemorySink { return &MemorySink{} }

// Write appends the event.
func (m *MemorySink) Write(e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

// Events returns a copy of all written events.
func (m *MemorySink) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event{}, m.events...)
}
