// Package dispatch is the core loop: poll a source, match alerts against
// subscribers, deliver each (alert, subscriber) pair exactly once.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/alert"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/metrics"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/notify"
	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/subscriber"
)

// Source is anything that can produce the current set of active alerts.
// The NWS REST API implements it today; an NWWS-OI (XMPP push) source can
// implement it later without touching the dispatcher.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]alert.Alert, error)
}

type Store interface {
	Delivered(ctx context.Context, alertID, subscriber string) (bool, error)
	RecordDelivery(ctx context.Context, alertID, subscriber, event string, at, expires time.Time) error
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}

// temporary is implemented by source errors that should be retried with
// backoff (rate limiting, server errors), optionally after a server-requested delay.
type temporary interface {
	Temporary() (retry bool, after time.Duration)
}

type Dispatcher struct {
	Source      Source
	Store       Store
	Notifier    notify.Notifier
	Subscribers []subscriber.Subscriber
	Metrics     *metrics.Metrics
	Log         *slog.Logger
	Now         func() time.Time // injectable for tests
	Location    *time.Location   // time zone for times shown in notifications; nil = local
	// OpsTopic receives "service degraded/recovered" messages when polling has
	// failed for longer than StaleAfter. Empty disables the watchdog.
	OpsTopic   string
	StaleAfter time.Duration
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// RunOnce performs a single poll-and-deliver cycle. Failed deliveries are not
// recorded, so they are retried on the next cycle.
func (d *Dispatcher) RunOnce(ctx context.Context) error {
	d.Metrics.Polls.Add(1)
	alerts, err := d.Source.Fetch(ctx)
	if err != nil {
		d.Metrics.PollErrors.Add(1)
		return fmt.Errorf("polling %s: %w", d.Source.Name(), err)
	}
	now := d.now()
	d.Metrics.ActiveAlerts.Store(int64(len(alerts)))
	d.Metrics.MarkSuccess(now)

	var errs []error
	for _, a := range alerts {
		if !a.Actual() || a.Expired(now) {
			continue
		}
		for _, s := range d.Subscribers {
			if !s.Matches(a) {
				continue
			}
			if err := d.deliver(ctx, a, s, now); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if _, err := d.Store.Prune(ctx, now.Add(-time.Hour)); err != nil {
		errs = append(errs, fmt.Errorf("pruning store: %w", err))
	}
	return errors.Join(errs...)
}

func (d *Dispatcher) deliver(ctx context.Context, a alert.Alert, s subscriber.Subscriber, now time.Time) error {
	done, err := d.Store.Delivered(ctx, a.ID, s.Name)
	if err != nil || done {
		return err
	}
	// Only announce a cancellation to someone who heard about the original.
	if a.MessageType == alert.MessageCancel {
		heard, err := d.deliveredAny(ctx, a.References, s.Name)
		if err != nil || !heard {
			return err
		}
	}
	msg := Format(a, s, d.Location)
	if err := d.Notifier.Send(ctx, msg); err != nil {
		d.Metrics.NotifyErrors.Add(1)
		return fmt.Errorf("notifying %s of %s: %w", s.Name, a.Event, err)
	}
	d.Metrics.Notifications.Add(1)
	d.Log.Info("delivered", "subscriber", s.Name, "event", a.Event, "type", a.MessageType,
		"priority", msg.Priority, "area", a.AreaDesc, "id", a.ID)
	// Recording after sending means a crash in between causes one duplicate
	// notification rather than a missed one, which is the right trade-off here.
	return d.Store.RecordDelivery(ctx, a.ID, s.Name, a.Event, now, a.Expires)
}

func (d *Dispatcher) deliveredAny(ctx context.Context, ids []string, sub string) (bool, error) {
	for _, id := range ids {
		if ok, err := d.Store.Delivered(ctx, id, sub); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// Run polls every interval until ctx is cancelled, backing off exponentially
// (up to 10 minutes) while the source is failing.
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	delay := time.Duration(0)
	failures := 0
	w := &watchdog{d: d, started: d.now()}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		err := d.RunOnce(ctx)
		w.check(ctx)
		delay = interval
		if err == nil {
			failures = 0
			continue
		}
		d.Log.Error("cycle failed", "err", err)
		var te temporary
		if errors.As(err, &te) {
			if retry, after := te.Temporary(); retry {
				failures++
				delay = max(min(interval<<min(failures, 6), 10*time.Minute), after)
			}
		}
	}
}

// watchdog tells a human when polling has been failing for a while, so a
// broken service can't be mistaken for a quiet weather day. It reports once
// when polling goes stale and once when it recovers.
type watchdog struct {
	d        *Dispatcher
	started  time.Time
	degraded bool
}

func (w *watchdog) check(ctx context.Context) {
	d := w.d
	if d.OpsTopic == "" || d.StaleAfter <= 0 {
		return
	}
	now := d.now()
	last := d.Metrics.LastSuccess()
	since := last
	if since.IsZero() {
		since = w.started
	}
	stale := now.Sub(since) > d.StaleAfter

	var msg notify.Message
	switch {
	case stale && !w.degraded:
		lastText := "since startup"
		if !last.IsZero() {
			lastText = "since " + last.In(d.loc()).Format("Mon 3:04 PM MST")
		}
		msg = notify.Message{
			Title:    "0x11WeatherAlerts is not receiving alerts",
			Body:     "No successful poll of the National Weather Service " + lastText + ". Weather alerts may be missed until this recovers.",
			Priority: subscriber.PriorityDefault,
			Tags:     []string{"warning", "satellite"},
		}
	case !stale && w.degraded:
		msg = notify.Message{
			Title:    "0x11WeatherAlerts recovered",
			Body:     "Polling the National Weather Service is working again.",
			Priority: subscriber.PriorityLow,
			Tags:     []string{"white_check_mark"},
		}
	default:
		return
	}
	msg.Topic = d.OpsTopic
	if err := d.Notifier.Send(ctx, msg); err != nil {
		d.Log.Error("watchdog notification failed", "err", err)
		return // retry on the next cycle
	}
	w.degraded = !w.degraded
	d.Log.Warn("watchdog", "title", msg.Title)
}

func (d *Dispatcher) loc() *time.Location {
	if d.Location != nil {
		return d.Location
	}
	return time.Local
}

// Format turns an alert into a phone notification for subscriber s, showing
// times in loc.
func Format(a alert.Alert, s subscriber.Subscriber, loc *time.Location) notify.Message {
	if loc == nil {
		loc = time.Local
	}
	title := a.Event
	switch a.MessageType {
	case alert.MessageUpdate:
		title = "Updated: " + title
	case alert.MessageCancel:
		title = "Cancelled: " + title
	}

	var b strings.Builder
	if a.Headline != "" {
		b.WriteString(a.Headline)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Area: %s\n", a.AreaDesc)
	if !a.Expires.IsZero() {
		fmt.Fprintf(&b, "Until: %s\n", a.Expires.In(loc).Format("Mon Jan 2 3:04 PM MST"))
	}
	if a.Instruction != "" {
		b.WriteString("\n")
		b.WriteString(truncate(a.Instruction, 600))
	}

	return notify.Message{
		Topic:    s.Topic,
		Title:    title,
		Body:     b.String(),
		Priority: s.Priority(a),
		Tags:     tags(a),
		Click:    a.URL,
	}
}

func tags(a alert.Alert) []string {
	t := []string{strings.ToLower(a.Severity.String())}
	ev := strings.ToLower(a.Event)
	switch {
	case strings.Contains(ev, "tornado"):
		t = append(t, "cyclone")
	case strings.Contains(ev, "thunderstorm"):
		t = append(t, "zap")
	case strings.Contains(ev, "flood"):
		t = append(t, "ocean")
	case strings.Contains(ev, "winter"), strings.Contains(ev, "snow"), strings.Contains(ev, "ice"), strings.Contains(ev, "blizzard"):
		t = append(t, "snowflake")
	case strings.Contains(ev, "wind"):
		t = append(t, "dash")
	case strings.Contains(ev, "heat"):
		t = append(t, "fire")
	default:
		t = append(t, "warning")
	}
	return t
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
