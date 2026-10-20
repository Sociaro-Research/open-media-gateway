package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestMonitoringStatsIncludeOnlyActiveUnleasedDueTasks(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTaskRepository(db)
	ctx := context.Background()
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	_, err := db.Exec(`INSERT INTO tasks(id,status,provider,model,prompt,created_at,worker_next_at,worker_lease_until,provider_owner_id,storage_owner_id)
	 SELECT id,status,'mock','mock','private prompt',created_at,next_at,lease_until,
	 '00000000-0000-0000-0000-000000000001'::uuid,'00000000-0000-0000-0000-000000000001'::uuid
	 FROM (VALUES
	 ($1::uuid,'queued',NOW()-INTERVAL '3 minutes',NOW()-INTERVAL '2 minutes',NULL),
	 ($2::uuid,'running',NOW()-INTERVAL '5 minutes',NOW()-INTERVAL '4 minutes',NOW()+INTERVAL '1 minute'),
	 ($3::uuid,'running',NOW()-INTERVAL '6 minutes',NOW()+INTERVAL '1 minute',NULL),
	 ($4::uuid,'running',NOW()-INTERVAL '7 minutes',NOW()-INTERVAL '3 minutes',NOW()-INTERVAL '1 minute'),
	 ($5::uuid,'succeeded',NOW()-INTERVAL '1 day',NOW()-INTERVAL '1 day',NULL)
	 ) AS fixture(id,status,created_at,next_at,lease_until)`, ids[0], ids[1], ids[2], ids[3], ids[4])
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO webhook_deliveries(id,task_id,event_type,webhook_url,payload,status,next_attempt_at) VALUES
	 ($1,$3,'task.created','https://private.example/webhook','{}','pending',NOW()-INTERVAL '2 minutes'),
	 ($2,$3,'task.created','https://private.example/webhook','{}','pending',NOW()+INTERVAL '1 minute')`, uuid.NewString(), uuid.NewString(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	stats, err := repo.MonitoringStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queued, running := stats.Tasks["queued"], stats.Tasks["running"]
	if queued.Count != 1 || queued.Due != 1 || running.Count != 3 || running.Due != 1 || stats.PendingWebhooks != 2 || len(stats.Tasks) != 2 {
		t.Fatalf("active counts: %+v", stats)
	}
	if queued.OldestAgeSeconds < 180 || queued.OldestOverdueSeconds < 120 || running.OldestAgeSeconds < 420 || running.OldestOverdueSeconds < 180 || stats.OldestWebhookSeconds < 120 {
		t.Fatalf("oldest ages: %+v", stats)
	}
	if _, err := db.Exec(`UPDATE tasks SET status='failed'; UPDATE webhook_deliveries SET status='delivered'`); err != nil {
		t.Fatal(err)
	}
	stats, err = repo.MonitoringStats(ctx)
	if err != nil || stats.Tasks["queued"] != (ActiveTaskStats{}) || stats.Tasks["running"] != (ActiveTaskStats{}) || stats.PendingWebhooks != 0 || stats.OldestWebhookSeconds != 0 {
		t.Fatalf("drained snapshot: %+v %v", stats, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.MonitoringStats(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot: %v", err)
	}
}
