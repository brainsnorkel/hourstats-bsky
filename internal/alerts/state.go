package alerts

import (
	"sync"
	"time"
)

// Active is one condition as /stats/health reports it: the condition plus the
// time it first held, so an operator can tell a condition that just appeared
// from one that has been latched for a day.
type Active struct {
	Name     string    `json:"name"`
	Severity string    `json:"severity"`
	Message  string    `json:"message"`
	Since    time.Time `json:"since"`
}

// State is the last evaluation, shared between the evaluating goroutine and
// the stats API. A nil *State behaves like an empty one, so the API can be
// started before the first evaluation (or in a test) without a nil check at
// every call site.
type State struct {
	mu     sync.RWMutex
	active []Active
	status string
	// since carries each condition's first-seen time across evaluations. A
	// name that drops out of one evaluation loses its entry, so the clock
	// restarts if it comes back — a recurrence is news.
	since map[string]time.Time
}

// NewState returns an empty state reporting StatusOK.
func NewState() *State {
	return &State{status: StatusOK, since: map[string]time.Time{}}
}

// Set replaces the recorded evaluation. now is passed in rather than read so
// the test owns the clock.
func (s *State) Set(conds []Condition, now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]time.Time, len(conds))
	active := make([]Active, 0, len(conds))
	for _, c := range conds {
		start, ok := s.since[c.Name]
		if !ok {
			start = now
		}
		seen[c.Name] = start
		active = append(active, Active{
			Name:     c.Name,
			Severity: c.Severity,
			Message:  c.Message,
			Since:    start,
		})
	}
	s.since = seen
	s.active = active
	s.status = Status(conds)
}

// Snapshot returns the recorded status and conditions. Both come from one
// critical section so the status can never disagree with the list beside it.
func (s *State) Snapshot() (string, []Active) {
	if s == nil {
		return StatusOK, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.status == "" {
		return StatusOK, nil
	}
	out := make([]Active, len(s.active))
	copy(out, s.active)
	return s.status, out
}
