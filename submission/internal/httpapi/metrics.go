package httpapi

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Metrics struct {
	pool *pgxpool.Pool

	accepted   atomic.Uint64
	duplicates atomic.Uint64
	rejected   atomic.Uint64
	overloaded atomic.Uint64
	inFlight   atomic.Int64
	peak       atomic.Int64
	ingest     latencySummary
	query      latencySummary
	alarm      latencySummary
}

type latencySummary struct {
	count atomic.Uint64
	total atomic.Uint64
	max   atomic.Uint64
}

type latencySnapshot struct {
	Count     uint64  `json:"count"`
	AverageMS float64 `json:"average_ms"`
	MaxMS     float64 `json:"max_ms"`
}

type metricsSnapshot struct {
	Events struct {
		Accepted   uint64 `json:"accepted"`
		Duplicates uint64 `json:"duplicates"`
		Rejected   uint64 `json:"rejected"`
		Overloaded uint64 `json:"overloaded"`
	} `json:"events"`
	Latency struct {
		Ingest        latencySnapshot `json:"ingest"`
		Query         latencySnapshot `json:"query"`
		AlarmDelivery latencySnapshot `json:"alarm_delivery"`
	} `json:"latency_ms"`
	HTTP struct {
		InFlight     int64 `json:"in_flight"`
		PeakInFlight int64 `json:"peak_in_flight"`
	} `json:"http"`
	PostgreSQL struct {
		AcquiredConnections int32 `json:"acquired_connections"`
		IdleConnections     int32 `json:"idle_connections"`
		TotalConnections    int32 `json:"total_connections"`
		MaxConnections      int32 `json:"max_connections"`
		EmptyAcquires       int64 `json:"empty_acquires"`
		CanceledAcquires    int64 `json:"canceled_acquires"`
	} `json:"postgresql"`
}

func NewMetrics(pool *pgxpool.Pool) *Metrics {
	return &Metrics{pool: pool}
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := m.inFlight.Add(1)
		updatePeak(&m.peak, current)
		defer m.inFlight.Add(-1)

		startedAt := time.Now()
		recorder := middleware.NewWrapResponseWriter(response, request.ProtoMajor)
		next.ServeHTTP(recorder, request)
		duration := time.Since(startedAt)

		if request.Method == http.MethodPost && request.URL.Path == "/events" {
			m.observeIngest(recorder.Status(), duration)
			return
		}
		if isQuery(request) {
			m.query.observe(duration)
		}
	})
}

func (m *Metrics) ObserveAlarmDelivery(duration time.Duration) {
	m.alarm.observe(duration)
}

func (m *Metrics) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	pool := m.pool.Stat()
	var snapshot metricsSnapshot
	snapshot.Events.Accepted = m.accepted.Load()
	snapshot.Events.Duplicates = m.duplicates.Load()
	snapshot.Events.Rejected = m.rejected.Load()
	snapshot.Events.Overloaded = m.overloaded.Load()
	snapshot.Latency.Ingest = m.ingest.snapshot()
	snapshot.Latency.Query = m.query.snapshot()
	snapshot.Latency.AlarmDelivery = m.alarm.snapshot()
	snapshot.HTTP.InFlight = m.inFlight.Load()
	snapshot.HTTP.PeakInFlight = m.peak.Load()
	snapshot.PostgreSQL.AcquiredConnections = pool.AcquiredConns()
	snapshot.PostgreSQL.IdleConnections = pool.IdleConns()
	snapshot.PostgreSQL.TotalConnections = pool.TotalConns()
	snapshot.PostgreSQL.MaxConnections = pool.MaxConns()
	snapshot.PostgreSQL.EmptyAcquires = pool.EmptyAcquireCount()
	snapshot.PostgreSQL.CanceledAcquires = pool.CanceledAcquireCount()
	writeJSON(response, http.StatusOK, snapshot)
}

func (m *Metrics) observeIngest(status int, duration time.Duration) {
	m.ingest.observe(duration)
	switch status {
	case http.StatusAccepted:
		m.accepted.Add(1)
	case http.StatusOK:
		m.duplicates.Add(1)
	default:
		if status >= http.StatusBadRequest {
			m.rejected.Add(1)
		}
		if status == http.StatusServiceUnavailable {
			m.overloaded.Add(1)
		}
	}
}

func (s *latencySummary) observe(duration time.Duration) {
	nanoseconds := uint64(max(duration.Nanoseconds(), 0))
	s.count.Add(1)
	s.total.Add(nanoseconds)
	for {
		previous := s.max.Load()
		if nanoseconds <= previous || s.max.CompareAndSwap(previous, nanoseconds) {
			return
		}
	}
}

func (s *latencySummary) snapshot() latencySnapshot {
	count := s.count.Load()
	total := s.total.Load()
	average := float64(0)
	if count > 0 {
		average = float64(total) / float64(count) / float64(time.Millisecond)
	}
	return latencySnapshot{
		Count:     count,
		AverageMS: average,
		MaxMS:     float64(s.max.Load()) / float64(time.Millisecond),
	}
}

func updatePeak(peak *atomic.Int64, current int64) {
	for {
		previous := peak.Load()
		if current <= previous || peak.CompareAndSwap(previous, current) {
			return
		}
	}
}

func isQuery(request *http.Request) bool {
	if request.Method != http.MethodGet || request.URL.Path == "/alarms/stream" {
		return false
	}
	return request.URL.Path == "/alarms" ||
		strings.HasPrefix(request.URL.Path, "/devices/") ||
		strings.HasPrefix(request.URL.Path, "/rooms/")
}
