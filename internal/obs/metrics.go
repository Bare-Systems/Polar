package obs

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Snapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	StartedAt   time.Time         `json:"started_at"`
	Collector   CollectorSnapshot `json:"collector"`
	Forecast    ForecastSnapshot  `json:"forecast"`
	Auth        AuthSnapshot      `json:"auth"`
	SLO         SLOSnapshot       `json:"slo"`
	Requests    []RequestSnapshot `json:"requests"`
}

type CollectorSnapshot struct {
	RunsTotal      int64     `json:"runs_total"`
	FailuresTotal  int64     `json:"failures_total"`
	ReadingsTotal  int64     `json:"readings_total"`
	LastSuccess    time.Time `json:"last_success"`
	LastFailure    time.Time `json:"last_failure"`
	LastDurationMS int64     `json:"last_duration_ms"`
}

type ForecastSnapshot struct {
	PullsTotal      int64     `json:"pulls_total"`
	FailuresTotal   int64     `json:"failures_total"`
	LastSuccess     time.Time `json:"last_success"`
	LastFailure     time.Time `json:"last_failure"`
	LastDurationMS  int64     `json:"last_duration_ms"`
	LastError       string    `json:"last_error,omitempty"`
	LastSuccessAgeS int64     `json:"last_success_age_seconds"`
	FreshUntil      time.Time `json:"fresh_until"`
	Stale           bool      `json:"stale"`
}

type AuthSnapshot struct {
	FailuresTotal int64 `json:"failures_total"`
}

type SLOSnapshot struct {
	BreachesTotal int64 `json:"breaches_total"`
}

type RequestSnapshot struct {
	Surface        string    `json:"surface"`
	Name           string    `json:"name"`
	RequestsTotal  int64     `json:"requests_total"`
	ErrorsTotal    int64     `json:"errors_total"`
	LastStatus     int       `json:"last_status"`
	LastDurationMS int64     `json:"last_duration_ms"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

type Metrics struct {
	startedAt time.Time

	collectorRuns      atomic.Int64
	collectorFailures  atomic.Int64
	collectorReadings  atomic.Int64
	forecastPulls      atomic.Int64
	forecastFailures   atomic.Int64
	authFailures       atomic.Int64
	auditWriteFailures atomic.Int64
	sloBreaches        atomic.Int64 // X-3: total freshness SLO breaches observed

	mu                    sync.RWMutex
	lastCollectorSuccess  time.Time
	lastCollectorFailure  time.Time
	lastCollectorDuration time.Duration
	lastForecastSuccess   time.Time
	lastForecastFailure   time.Time
	lastForecastDuration  time.Duration
	lastForecastError     string
	requests              map[string]*requestCounters
}

type requestCounters struct {
	surface        string
	name           string
	requestsTotal  int64
	errorsTotal    int64
	lastStatus     int
	lastDurationMS int64
	lastSeenAt     time.Time
}

func NewMetrics() *Metrics {
	return &Metrics{
		startedAt: time.Now().UTC(),
		requests:  make(map[string]*requestCounters),
	}
}

func (m *Metrics) RecordCollectorRun(readings int, err error, duration time.Duration) {
	m.collectorRuns.Add(1)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastCollectorDuration = duration
	if err != nil {
		m.collectorFailures.Add(1)
		m.lastCollectorFailure = time.Now().UTC()
		return
	}

	m.collectorReadings.Add(int64(readings))
	m.lastCollectorSuccess = time.Now().UTC()
}

func (m *Metrics) RecordForecastRun(points int, err error, duration time.Duration) {
	m.forecastPulls.Add(1)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastForecastDuration = duration
	if err != nil {
		m.forecastFailures.Add(1)
		m.lastForecastFailure = time.Now().UTC()
		m.lastForecastError = err.Error()
		return
	}

	m.lastForecastSuccess = time.Now().UTC()
	m.lastForecastError = ""
}

func (m *Metrics) RecordAuthFailure() {
	m.authFailures.Add(1)
}

// RecordSLOBreach increments the total SLO breach counter (X-3).
func (m *Metrics) RecordSLOBreach() {
	m.sloBreaches.Add(1)
}

func (m *Metrics) RecordRequest(surface, name string, status int, duration time.Duration) {
	key := surface + ":" + name

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.requests[key]
	if !ok {
		entry = &requestCounters{surface: surface, name: name}
		m.requests[key] = entry
	}
	entry.requestsTotal++
	if status >= 400 {
		entry.errorsTotal++
	}
	entry.lastStatus = status
	entry.lastDurationMS = duration.Milliseconds()
	entry.lastSeenAt = time.Now().UTC()
}

func (m *Metrics) Snapshot(now time.Time, forecastInterval time.Duration) Snapshot {
	m.mu.RLock()
	collectorLastSuccess := m.lastCollectorSuccess
	collectorLastFailure := m.lastCollectorFailure
	collectorLastDuration := m.lastCollectorDuration
	forecastLastSuccess := m.lastForecastSuccess
	forecastLastFailure := m.lastForecastFailure
	forecastLastDuration := m.lastForecastDuration
	forecastLastError := m.lastForecastError
	requests := make([]RequestSnapshot, 0, len(m.requests))
	for _, entry := range m.requests {
		requests = append(requests, RequestSnapshot{
			Surface:        entry.surface,
			Name:           entry.name,
			RequestsTotal:  entry.requestsTotal,
			ErrorsTotal:    entry.errorsTotal,
			LastStatus:     entry.lastStatus,
			LastDurationMS: entry.lastDurationMS,
			LastSeenAt:     entry.lastSeenAt,
		})
	}
	m.mu.RUnlock()

	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Surface == requests[j].Surface {
			return requests[i].Name < requests[j].Name
		}
		return requests[i].Surface < requests[j].Surface
	})

	forecastFreshUntil := time.Time{}
	forecastAge := int64(0)
	stale := true
	if !forecastLastSuccess.IsZero() {
		forecastFreshUntil = forecastLastSuccess.Add(2 * forecastInterval)
		forecastAge = int64(now.Sub(forecastLastSuccess).Seconds())
		stale = now.After(forecastFreshUntil)
	}
	if !forecastLastFailure.IsZero() && forecastLastFailure.After(forecastLastSuccess) {
		stale = true
	}

	return Snapshot{
		GeneratedAt: now,
		StartedAt:   m.startedAt,
		Collector: CollectorSnapshot{
			RunsTotal:      m.collectorRuns.Load(),
			FailuresTotal:  m.collectorFailures.Load(),
			ReadingsTotal:  m.collectorReadings.Load(),
			LastSuccess:    collectorLastSuccess,
			LastFailure:    collectorLastFailure,
			LastDurationMS: collectorLastDuration.Milliseconds(),
		},
		Forecast: ForecastSnapshot{
			PullsTotal:      m.forecastPulls.Load(),
			FailuresTotal:   m.forecastFailures.Load(),
			LastSuccess:     forecastLastSuccess,
			LastFailure:     forecastLastFailure,
			LastDurationMS:  forecastLastDuration.Milliseconds(),
			LastError:       forecastLastError,
			LastSuccessAgeS: forecastAge,
			FreshUntil:      forecastFreshUntil,
			Stale:           stale,
		},
		Auth: AuthSnapshot{
			FailuresTotal: m.authFailures.Load(),
		},
		SLO: SLOSnapshot{
			BreachesTotal: m.sloBreaches.Load(),
		},
		Requests: requests,
	}
}
