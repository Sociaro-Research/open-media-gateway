package monitoring

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type dependencyCollector struct {
	deps Dependencies
	desc map[string]*prometheus.Desc
}

func newDependencyCollector(deps Dependencies) *dependencyCollector {
	c := &dependencyCollector{deps: deps, desc: make(map[string]*prometheus.Desc)}
	for _, metric := range []struct {
		name, help string
		labels     []string
	}{
		{"dependency_up", "Dependency reachable from this process (1) or unavailable (0).", []string{"dependency"}},
		{"collector_success", "Operational snapshot succeeded (1) or failed (0); failed snapshot values are omitted.", []string{"collector"}},
		{"database_connections", "Database pool connections by state.", []string{"state"}},
		{"database_max_open_connections", "Database pool connection limit.", nil},
		{"database_connection_waits_total", "Database pool waits for a free connection.", nil},
		{"database_connection_wait_seconds_total", "Time spent waiting for database pool connections.", nil},
		{"queue_jobs", "Redis delivery entries by state; claimed includes expired leases.", []string{"queue", "state"}},
		{"active_tasks", "Durable nonterminal PostgreSQL tasks by status.", []string{"status"}},
		{"oldest_active_task_age_seconds", "Age since admission of the oldest nonterminal task.", []string{"status"}},
		{"tasks_due", "Tasks due for processing without an unexpired database lease.", []string{"status"}},
		{"oldest_task_overdue_seconds", "Time since scheduled processing of the oldest due, unleased task.", []string{"status"}},
		{"pending_webhooks", "Durable webhook deliveries awaiting completion.", nil},
		{"oldest_webhook_overdue_seconds", "Time since scheduled delivery of the oldest pending due webhook.", nil},
	} {
		c.desc[metric.name] = prometheus.NewDesc("media_gateway_"+metric.name, metric.help, metric.labels, nil)
	}
	return c
}

func (c *dependencyCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.desc {
		ch <- desc
	}
}

func (c *dependencyCollector) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, kind prometheus.ValueType, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(c.desc[name], kind, value, labels...)
	}
	up := func(name string, err error) {
		value := 1.0
		if err != nil {
			value = 0
		}
		emit("collector_success", prometheus.GaugeValue, value, name)
	}
	// Separate bounded reads keep a failed database from suppressing Redis data.
	var wg sync.WaitGroup
	run := func(f func(context.Context)) {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			f(ctx)
		})
	}
	if c.deps.Database != nil {
		stats := c.deps.Database.Stats()
		emit("database_connections", prometheus.GaugeValue, float64(stats.OpenConnections), "open")
		emit("database_connections", prometheus.GaugeValue, float64(stats.InUse), "in_use")
		emit("database_connections", prometheus.GaugeValue, float64(stats.Idle), "idle")
		emit("database_max_open_connections", prometheus.GaugeValue, float64(stats.MaxOpenConnections))
		emit("database_connection_waits_total", prometheus.CounterValue, float64(stats.WaitCount))
		emit("database_connection_wait_seconds_total", prometheus.CounterValue, stats.WaitDuration.Seconds())
		run(func(ctx context.Context) {
			value := 1.0
			if c.deps.Database.PingContext(ctx) != nil {
				value = 0
			}
			emit("dependency_up", prometheus.GaugeValue, value, "postgres")
		})
	}
	for name, queue := range c.deps.Queues {
		run(func(ctx context.Context) {
			stats, err := queue.Stats(ctx)
			up("queue_"+name, err)
			value := 1.0
			if err != nil {
				value = 0
			}
			emit("dependency_up", prometheus.GaugeValue, value, "redis_"+name)
			if err == nil {
				emit("queue_jobs", prometheus.GaugeValue, float64(stats.Pending), name, "pending")
				emit("queue_jobs", prometheus.GaugeValue, float64(stats.Claimed), name, "claimed")
				emit("queue_jobs", prometheus.GaugeValue, float64(stats.Expired), name, "expired")
			}
		})
	}
	if c.deps.Tasks != nil {
		run(func(ctx context.Context) {
			stats, err := c.deps.Tasks(ctx)
			up("tasks", err)
			if err != nil {
				return
			}
			for _, status := range []string{"queued", "running"} {
				value := stats.Tasks[status]
				emit("active_tasks", prometheus.GaugeValue, float64(value.Count), status)
				emit("oldest_active_task_age_seconds", prometheus.GaugeValue, value.OldestAgeSeconds, status)
				emit("tasks_due", prometheus.GaugeValue, float64(value.Due), status)
				emit("oldest_task_overdue_seconds", prometheus.GaugeValue, value.OldestOverdueSeconds, status)
			}
			emit("pending_webhooks", prometheus.GaugeValue, float64(stats.PendingWebhooks))
			emit("oldest_webhook_overdue_seconds", prometheus.GaugeValue, stats.OldestWebhookSeconds)
		})
	}
	wg.Wait()
}
