// Package metrics exposes a handful of counters in Prometheus text format.
// Small enough that pulling in the Prometheus client library isn't worth it.
package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Polls             atomic.Int64
	PollErrors        atomic.Int64
	ActiveAlerts      atomic.Int64
	Notifications     atomic.Int64
	NotifyErrors      atomic.Int64
	LastSuccessUnixMs atomic.Int64
}

func (m *Metrics) MarkSuccess(t time.Time) { m.LastSuccessUnixMs.Store(t.UnixMilli()) }

func (m *Metrics) LastSuccess() time.Time {
	ms := m.LastSuccessUnixMs.Load()
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	write := func(name, typ, help string, v int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %d\n", name, help, name, typ, name, v)
	}
	write("weatheralerts_polls_total", "counter", "Source polls attempted.", m.Polls.Load())
	write("weatheralerts_poll_errors_total", "counter", "Source polls that failed.", m.PollErrors.Load())
	write("weatheralerts_active_alerts", "gauge", "Active alerts returned by the last successful poll.", m.ActiveAlerts.Load())
	write("weatheralerts_notifications_total", "counter", "Notifications delivered.", m.Notifications.Load())
	write("weatheralerts_notification_errors_total", "counter", "Notification deliveries that failed.", m.NotifyErrors.Load())
	fmt.Fprintf(w, "# HELP weatheralerts_last_success_seconds Unix time of the last successful poll.\n# TYPE weatheralerts_last_success_seconds gauge\nweatheralerts_last_success_seconds %.3f\n",
		float64(m.LastSuccessUnixMs.Load())/1000)
}
