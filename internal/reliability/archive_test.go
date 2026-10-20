package reliability_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestReliabilityArchiveBurstDoesNotBlockSubmissions(t *testing.T) {
	s := newStack(t, 30*time.Second)
	s.fixture.ready = false
	s.fixture.media = bytes.Repeat([]byte("a"), 1<<20)
	gate := make(chan struct{})
	s.fixture.archiveGate = gate
	closed := false
	defer func() {
		if !closed {
			close(gate)
		}
	}()
	ids := []string{}
	for i := 0; i < 32; i++ {
		ids = append(ids, s.submit("archive-burst", fmt.Sprint(i), false))
	}
	s.startWorkers(1, 20*time.Millisecond)
	s.fixture.release()
	eventually(t, s.ctx, "archive slots did not fill", func() bool { s.fixture.mu.Lock(); defer s.fixture.mu.Unlock(); return s.fixture.activeArchives == 4 })
	id := s.submit("new-during-archive", "new", false)
	ids = append(ids, id)
	eventually(t, s.ctx, "archival blocked new provider submission", func() bool { return s.fixture.job(id).Submissions == 1 })
	s.fixture.mu.Lock()
	peak := s.fixture.peakArchives
	s.fixture.mu.Unlock()
	if peak != 4 {
		t.Fatalf("archive concurrency was not bounded: %d", peak)
	}
	close(gate)
	closed = true
	for _, id := range ids {
		s.checkFile(s.terminal(id))
	}
	t.Log("33 real 1 MiB archives downloaded and verified; submissions remained available with all 4 archive slots occupied")
}
