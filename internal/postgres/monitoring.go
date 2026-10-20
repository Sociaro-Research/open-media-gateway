package postgres

import "context"

type ActiveTaskStats struct {
	Count                int64
	OldestAgeSeconds     float64
	Due                  int64
	OldestOverdueSeconds float64
}

type MonitoringStats struct {
	Tasks                map[string]ActiveTaskStats
	PendingWebhooks      int64
	OldestWebhookSeconds float64
}

// MonitoringStats reads only active rows, using the existing partial indexes.
// It never scans completed task history or reads request/customer data.
func (r *TaskRepository) MonitoringStats(ctx context.Context) (MonitoringStats, error) {
	result := MonitoringStats{Tasks: map[string]ActiveTaskStats{"queued": {}, "running": {}}}
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*),
	 GREATEST(0, EXTRACT(EPOCH FROM NOW()-MIN(created_at))),
	 COUNT(*) FILTER (WHERE worker_next_at<=NOW() AND (worker_lease_until IS NULL OR worker_lease_until<=NOW())),
	 COALESCE(GREATEST(0, EXTRACT(EPOCH FROM NOW()-MIN(worker_next_at)
	 FILTER (WHERE worker_next_at<=NOW() AND (worker_lease_until IS NULL OR worker_lease_until<=NOW())))),0)
	 FROM tasks WHERE status IN ('queued','running') GROUP BY status`)
	if err != nil {
		return MonitoringStats{}, err
	}
	for rows.Next() {
		var status string
		var stats ActiveTaskStats
		if err := rows.Scan(&status, &stats.Count, &stats.OldestAgeSeconds, &stats.Due, &stats.OldestOverdueSeconds); err != nil {
			rows.Close()
			return MonitoringStats{}, err
		}
		result.Tasks[status] = stats
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return MonitoringStats{}, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*),
	 COALESCE(GREATEST(0, EXTRACT(EPOCH FROM NOW()-MIN(next_attempt_at) FILTER (WHERE next_attempt_at<=NOW()))),0)
	 FROM webhook_deliveries WHERE status='pending'`).Scan(&result.PendingWebhooks, &result.OldestWebhookSeconds)
	return result, err
}
