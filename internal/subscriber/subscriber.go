// Package subscriber decides which alerts a subscriber cares about and how
// loudly to deliver them.
package subscriber

import (
	"strings"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/alert"
)

// ntfy priorities: 5 = urgent (bypasses Do Not Disturb, shows on the lock
// screen), 4 = high, 3 = default, 2 = low.
const (
	PriorityLow     = 2
	PriorityDefault = 3
	PriorityHigh    = 4
	PriorityUrgent  = 5
)

type Subscriber struct {
	Name  string `yaml:"name"`
	Topic string `yaml:"topic"`

	// Zones are UGC codes; an alert matches if it covers any of them.
	// Use both the county (OHC063) and forecast zone (OHZ017): storm warnings
	// are issued by county, winter/wind products by forecast zone.
	Zones []string `yaml:"zones"`

	MinSeverity   string   `yaml:"min_severity"`   // minor | moderate | severe | extreme
	Events        []string `yaml:"events"`         // allowlist; empty = all events
	ExcludeEvents []string `yaml:"exclude_events"` // always dropped
	UrgentEvents  []string `yaml:"urgent_events"`  // always delivered at urgent priority
	SkipUpdates   bool     `yaml:"skip_updates"`   // ignore Update messages for alerts already sent
}

// Matches reports whether a should be delivered to s.
func (s Subscriber) Matches(a alert.Alert) bool {
	if !overlaps(s.Zones, a.Zones) {
		return false
	}
	if containsFold(s.ExcludeEvents, a.Event) {
		return false
	}
	if a.MessageType == alert.MessageUpdate && s.SkipUpdates {
		return false
	}
	// Urgent events bypass every other filter: a Tornado Warning should never
	// be lost to a severity threshold.
	if containsFold(s.UrgentEvents, a.Event) {
		return true
	}
	if len(s.Events) > 0 && !containsFold(s.Events, a.Event) {
		return false
	}
	return a.Severity >= alert.ParseSeverity(s.MinSeverity)
}

// Priority maps an alert to an ntfy priority for this subscriber.
func (s Subscriber) Priority(a alert.Alert) int {
	if containsFold(s.UrgentEvents, a.Event) {
		return PriorityUrgent
	}
	switch a.Severity {
	case alert.SeverityExtreme:
		return PriorityUrgent
	case alert.SeveritySevere:
		return PriorityHigh
	case alert.SeverityMinor:
		return PriorityLow
	default:
		return PriorityDefault
	}
}

func overlaps(want, have []string) bool {
	for _, w := range want {
		if containsFold(have, w) {
			return true
		}
	}
	return false
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
