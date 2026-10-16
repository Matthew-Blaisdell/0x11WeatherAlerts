package dispatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/alert"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/metrics"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/notify"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/store"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/subscriber"
)

type fakeSource struct{ alerts []alert.Alert }

func (f *fakeSource) Name() string                                 { return "fake" }
func (f *fakeSource) Fetch(context.Context) ([]alert.Alert, error) { return f.alerts, nil }

type fakeNotifier struct {
	sent []notify.Message
	fail bool
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	if f.fail {
		return errors.New("ntfy down")
	}
	f.sent = append(f.sent, m)
	return nil
}

var now = time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC)

func newDispatcher(t *testing.T, src *fakeSource, n *fakeNotifier) *Dispatcher {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Dispatcher{
		Source:   src,
		Store:    st,
		Notifier: n,
		Subscribers: []subscriber.Subscriber{{
			Name: "home", Topic: "wx-home", Zones: []string{"OHC063"},
			MinSeverity: "moderate", UrgentEvents: []string{"Tornado Warning"},
		}},
		Metrics: &metrics.Metrics{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
	}
}

func warning(id string, mt alert.MessageType, refs ...string) alert.Alert {
	return alert.Alert{
		ID: id, MessageType: mt, References: refs, Status: "Actual",
		Event: "Tornado Warning", Severity: alert.SeverityExtreme,
		Zones: []string{"OHC063"}, AreaDesc: "Hancock, OH",
		Expires: now.Add(30 * time.Minute),
	}
}

func TestDeliversEachAlertOnceAcrossPolls(t *testing.T) {
	src := &fakeSource{alerts: []alert.Alert{warning("a1", alert.MessageAlert)}}
	n := &fakeNotifier{}
	d := newDispatcher(t, src, n)

	for range 3 {
		if err := d.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(n.sent) != 1 {
		t.Fatalf("sent %d notifications, want 1", len(n.sent))
	}
	m := n.sent[0]
	if m.Topic != "wx-home" || m.Title != "Tornado Warning" || m.Priority != subscriber.PriorityUrgent {
		t.Errorf("unexpected message %+v", m)
	}
}

func TestFailedDeliveryIsRetried(t *testing.T) {
	src := &fakeSource{alerts: []alert.Alert{warning("a1", alert.MessageAlert)}}
	n := &fakeNotifier{fail: true}
	d := newDispatcher(t, src, n)

	if err := d.RunOnce(context.Background()); err == nil {
		t.Fatal("expected delivery error")
	}
	n.fail = false
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 1 {
		t.Fatalf("sent %d, want 1 after recovery", len(n.sent))
	}
	if got := d.Metrics.NotifyErrors.Load(); got != 1 {
		t.Errorf("notify errors = %d, want 1", got)
	}
}

func TestSkipsExpiredAndNonActual(t *testing.T) {
	expired := warning("old", alert.MessageAlert)
	expired.Expires = now.Add(-time.Minute)
	exercise := warning("drill", alert.MessageAlert)
	exercise.Status = "Exercise"

	n := &fakeNotifier{}
	d := newDispatcher(t, &fakeSource{alerts: []alert.Alert{expired, exercise}}, n)
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 0 {
		t.Fatalf("sent %v, want nothing", n.sent)
	}
}

func TestUpdatesAndCancellations(t *testing.T) {
	src := &fakeSource{}
	n := &fakeNotifier{}
	d := newDispatcher(t, src, n)
	ctx := context.Background()

	// A cancellation for an alert this subscriber never received is noise.
	src.alerts = []alert.Alert{warning("c0", alert.MessageCancel, "never-sent")}
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 0 {
		t.Fatalf("cancel for unknown alert was sent: %+v", n.sent)
	}

	src.alerts = []alert.Alert{warning("a1", alert.MessageAlert)}
	src.alerts = append(src.alerts, warning("u1", alert.MessageUpdate, "a1"))
	src.alerts = append(src.alerts, warning("c1", alert.MessageCancel, "u1"))
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, m := range n.sent {
		titles = append(titles, m.Title)
	}
	want := []string{"Tornado Warning", "Updated: Tornado Warning", "Cancelled: Tornado Warning"}
	if len(titles) != len(want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("titles[%d] = %q, want %q", i, titles[i], want[i])
		}
	}
}

type failingSource struct{ fail bool }

func (f *failingSource) Name() string { return "flaky" }
func (f *failingSource) Fetch(context.Context) ([]alert.Alert, error) {
	if f.fail {
		return nil, errors.New("api down")
	}
	return nil, nil
}

func TestWatchdogReportsStaleAndRecoveredOnce(t *testing.T) {
	clock := now
	src := &failingSource{fail: true}
	n := &fakeNotifier{}
	d := newDispatcher(t, nil, n)
	d.Source = src
	d.Now = func() time.Time { return clock }
	d.OpsTopic, d.StaleAfter = "ops", 15*time.Minute
	w := &watchdog{d: d, started: clock}
	ctx := context.Background()

	step := func(advance time.Duration) {
		clock = clock.Add(advance)
		d.RunOnce(ctx)
		w.check(ctx)
	}
	step(10 * time.Minute) // failing, but not stale yet
	if len(n.sent) != 0 {
		t.Fatalf("reported too early: %+v", n.sent)
	}
	step(10 * time.Minute) // stale
	step(5 * time.Minute)  // still stale: no repeat
	if len(n.sent) != 1 || n.sent[0].Topic != "ops" {
		t.Fatalf("want exactly one degraded message to ops, got %+v", n.sent)
	}
	src.fail = false
	step(time.Minute)
	step(time.Minute)
	if len(n.sent) != 2 || n.sent[1].Title != "0x11WeatherAlerts recovered" {
		t.Fatalf("want one recovery message, got %+v", n.sent)
	}
}
