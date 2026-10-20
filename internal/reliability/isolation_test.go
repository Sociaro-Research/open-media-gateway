package reliability_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
)

func TestReliabilityBusyOwnerDoesNotBlockAnotherOwner(t *testing.T) {
	s := newStack(t, 30*time.Second)
	s.personalKey()
	firstKey := s.key
	s.fixture.ready = false
	busy := []string{}
	for i := 0; i < 100; i++ {
		busy = append(busy, s.submit("busy-owner", fmt.Sprint(i), false))
	}
	s.startWorkers(2, 100*time.Millisecond)
	eventually(t, s.ctx, "busy requests not submitted", func() bool { return len(s.fixture.snapshot()) == 100 })
	other, err := postgres.NewAuthRepository(s.db).UpsertGoogleUser(s.ctx, "normal-user", "normal@example.test", "Normal", "")
	if err != nil {
		t.Fatal(err)
	}
	s.key = issueKey(t, s, other.ID)
	status, _, err := s.request("GET", "/v1/generations/"+busy[0], "", "")
	if err != nil || status != 404 {
		t.Fatalf("cross-owner content exposed: status=%d err=%v", status, err)
	}
	for i := 0; i < 5; i++ {
		s.checkFile(s.terminal(s.submit("fast", fmt.Sprintf("normal-%d", i), false)))
	}
	for _, id := range busy {
		task, err := s.repo.Get(s.ctx, id)
		if err != nil || task.StoredAt != nil {
			t.Fatalf("busy fixture ended instead of remaining pending: %v", err)
		}
	}
	s.key = firstKey
	s.fixture.release()
	for _, id := range busy {
		s.checkFile(s.terminal(id))
	}
}
