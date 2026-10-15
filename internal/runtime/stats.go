// Package runtime holds lightweight shared runtime state (statistics) that is
// written by the poller and read by the bot's /status command. Keeping it in
// its own package avoids an import cycle between poller and bot.
package runtime

import (
	"sync"
	"time"
)

// Stats is a concurrency-safe container for lightweight runtime counters.
type Stats struct {
	mu                sync.RWMutex
	lastPoll          time.Time
	pollCount         int
	notificationCount int
	canvasOK          bool
	canvasErr         string
	canvasCheckedAt   time.Time
}

// New returns an empty Stats.
func New() *Stats { return &Stats{} }

// SetLastPoll records the time of the most recent poll iteration.
func (s *Stats) SetLastPoll(t time.Time) {
	s.mu.Lock()
	s.lastPoll = t
	s.mu.Unlock()
}

// IncPoll increments the completed-poll counter.
func (s *Stats) IncPoll() {
	s.mu.Lock()
	s.pollCount++
	s.mu.Unlock()
}

// AddNotifications adds n to the number of notifications delivered.
func (s *Stats) AddNotifications(n int) {
	if n == 0 {
		return
	}
	s.mu.Lock()
	s.notificationCount += n
	s.mu.Unlock()
}

// SetCanvasOK records the result of the last successful Canvas interaction.
func (s *Stats) SetCanvasOK(ok bool, errMsg string, at time.Time) {
	s.mu.Lock()
	s.canvasOK = ok
	s.canvasErr = errMsg
	s.canvasCheckedAt = at
	s.mu.Unlock()
}

// Snapshot is an immutable copy of Stats.
type Snapshot struct {
	LastPoll        time.Time
	PollCount       int
	Notifications   int
	CanvasOK        bool
	CanvasErr       string
	CanvasCheckedAt time.Time
}

// Snapshot returns a consistent copy of the current statistics.
func (s *Stats) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		LastPoll:        s.lastPoll,
		PollCount:       s.pollCount,
		Notifications:   s.notificationCount,
		CanvasOK:        s.canvasOK,
		CanvasErr:       s.canvasErr,
		CanvasCheckedAt: s.canvasCheckedAt,
	}
}
