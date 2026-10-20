// Package monitoring exposes bounded, installation-level operational metrics.
package monitoring

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Database interface {
	PingContext(context.Context) error
	Stats() sql.DBStats
}

type Queue interface {
	Stats(context.Context) (queue.Stats, error)
}

type Dependencies struct {
	Database Database
	Queues   map[string]Queue
	Tasks    func(context.Context) (postgres.MonitoringStats, error)
}

type Metrics struct {
	registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	inFlight      prometheus.Gauge
	loopSuccess   *prometheus.GaugeVec
	loopErrors    *prometheus.CounterVec
	loopStale     *prometheus.GaugeVec
	activeClaims  prometheus.Gauge
	claims        *prometheus.CounterVec
	claimDuration *prometheus.HistogramVec
}

func New(service string, deps Dependencies) *Metrics {
	registry := prometheus.NewRegistry()
	register := prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registry)
	register.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{registry: registry}
	if service == "gateway" {
		m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "media_gateway_http_requests_total", Help: "HTTP requests by bounded method, registered route pattern and response status."}, []string{"method", "route", "status"})
		m.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "media_gateway_http_request_duration_seconds", Help: "HTTP request duration including response body delivery.", Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 15, 30, 60, 300}}, []string{"method", "route"})
		m.inFlight = prometheus.NewGauge(prometheus.GaugeOpts{Name: "media_gateway_http_requests_in_flight", Help: "Currently executing HTTP requests."})
		register.MustRegister(m.requests, m.duration, m.inFlight)
	}
	if service == "worker" {
		m.loopSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "media_gateway_worker_loop_last_success_timestamp_seconds", Help: "Last successful dispatcher/consumer iteration, including an empty queue."}, []string{"loop"})
		m.loopErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "media_gateway_worker_loop_errors_total", Help: "Dispatcher/consumer iteration failures."}, []string{"loop"})
		m.loopStale = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "media_gateway_worker_loop_stale_after_seconds", Help: "Expected maximum interval between successful worker loop iterations."}, []string{"loop"})
		m.activeClaims = prometheus.NewGauge(prometheus.GaugeOpts{Name: "media_gateway_worker_active_claims", Help: "Current task claim operations, including provider calls and archival."})
		m.claims = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "media_gateway_worker_claims_total", Help: "Finished claim operations; success does not imply a terminal task."}, []string{"outcome"})
		m.claimDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "media_gateway_worker_claim_duration_seconds", Help: "Duration of a task claim operation.", Buckets: []float64{.01, .1, 1, 5, 15, 60, 300, 600, 1500}}, []string{"outcome"})
		register.MustRegister(m.loopSuccess, m.loopErrors, m.loopStale, m.activeClaims, m.claims, m.claimDuration)
		for _, loop := range []string{"dispatcher", "consumer"} {
			m.loopSuccess.WithLabelValues(loop).Set(0)
			m.loopErrors.WithLabelValues(loop).Add(0)
			m.loopStale.WithLabelValues(loop).Set(60)
		}
		for _, outcome := range []string{"success", "error"} {
			m.claims.WithLabelValues(outcome).Add(0)
		}
	}
	register.MustRegister(newDependencyCollector(deps))
	return m
}

func (m *Metrics) SetWorkerDispatchInterval(interval time.Duration) {
	m.loopStale.WithLabelValues("dispatcher").Set(max(60, 3*interval.Seconds()))
}

func (m *Metrics) ObserveWorkerLoop(loop string, err error) {
	if err != nil {
		m.loopErrors.WithLabelValues(loop).Inc()
		return
	}
	m.loopSuccess.WithLabelValues(loop).SetToCurrentTime()
}

func (m *Metrics) WorkerClaimStarted() func(error) {
	started := time.Now()
	m.activeClaims.Inc()
	return func(err error) {
		m.activeClaims.Dec()
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		m.claims.WithLabelValues(outcome).Inc()
		m.claimDuration.WithLabelValues(outcome).Observe(time.Since(started).Seconds())
	}
}

func (m *Metrics) InstrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		m.inFlight.Inc()
		writer := &statusWriter{ResponseWriter: w}
		completed := false
		defer func() {
			m.inFlight.Dec()
			status := writer.status
			if status == 0 {
				status = http.StatusOK
				if !completed {
					status = http.StatusInternalServerError
				}
			}
			// ServeMux fills Pattern with the registered template after matching.
			// Raw paths, query strings, IDs, hosts and credentials are never labels.
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			method := r.Method
			switch method {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
			default:
				method = "OTHER"
			}
			m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
			m.duration.WithLabelValues(method, route).Observe(time.Since(started).Seconds())
		}()
		next.ServeHTTP(writer, r)
		completed = true
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err == nil && w.status == 0 {
		w.status = http.StatusOK
	}
	return err
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 4 * time.Second}))
	return mux
}

type Server struct {
	server *http.Server
	Errors <-chan error
}

// Listen uses a dedicated socket, never the public API/UI listener.
func (m *Metrics) Listen(address string) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Addr: listener.Addr().String(), Handler: m.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	errors := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errors <- err
		}
	}()
	return &Server{server: server, Errors: errors}, nil
}

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}
