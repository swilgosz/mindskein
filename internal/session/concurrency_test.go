package session

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestEndSurvivesAConcurrentStop is the race the sticky-ended guard was
// written for and did not cover: the guard reads the in-memory copy, so a Stop
// that loaded the record before SessionEnd wrote it never sees the ended
// status at all and renames "done" over a finished session.
//
// Run over many rounds because a race that reproduces sometimes is still a
// bug; with the write serialised, ended must win every round whichever order
// the two land in.
func TestEndSurvivesAConcurrentStop(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	now := time.Now().UTC()

	for round := 0; round < 200; round++ {
		id := fmt.Sprintf("sess%04d", round)
		if _, err := store.Upsert(id, now, func(s *Session) {
			s.Status = StatusRunning
		}); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			store.Upsert(id, now, func(s *Session) {
				if s.Status == StatusEnded {
					return
				}
				s.Status = StatusDone
			})
		}()
		go func() {
			defer wg.Done()
			store.Upsert(id, now, func(s *Session) { s.Status = StatusEnded })
		}()
		wg.Wait()

		got, err := store.Load(id)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if got.Status != StatusEnded {
			t.Fatalf("round %d: status = %q, want it to stay %q — a late Stop resurrected a finished session",
				round, got.Status, StatusEnded)
		}
	}
}

// BenchmarkUpsert measures the whole load-mutate-save that PreToolUse runs on
// every single tool call. The lock is on this path, so its cost is the thing
// that decides whether it may stay.
func BenchmarkUpsert(b *testing.B) {
	store := &Store{Dir: b.TempDir()}
	now := time.Now().UTC()
	for i := 0; i < b.N; i++ {
		if _, err := store.Upsert("bench0001", now, func(s *Session) {
			s.Status = StatusRunning
			s.LastEvent = "Edit"
		}); err != nil {
			b.Fatal(err)
		}
	}
}
