package subscriber

import (
	"testing"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/alert"
)

func TestMatchesAndPriority(t *testing.T) {
	home := Subscriber{
		Name:          "home",
		Zones:         []string{"OHC063", "OHZ017"},
		MinSeverity:   "moderate",
		ExcludeEvents: []string{"Special Weather Statement"},
		UrgentEvents:  []string{"Tornado Warning"},
	}
	at := func(event string, sev alert.Severity, zones ...string) alert.Alert {
		return alert.Alert{Event: event, Severity: sev, Zones: zones, MessageType: alert.MessageAlert}
	}

	tests := []struct {
		name     string
		sub      Subscriber
		alert    alert.Alert
		match    bool
		priority int
	}{
		{"county match", home, at("Severe Thunderstorm Warning", alert.SeveritySevere, "OHC063"), true, PriorityHigh},
		{"zone match, case-insensitive", home, at("Winter Storm Warning", alert.SeverityModerate, "ohz017"), true, PriorityDefault},
		{"other county", home, at("Severe Thunderstorm Warning", alert.SeveritySevere, "OHC001"), false, 0},
		{"below min severity", home, at("Frost Advisory", alert.SeverityMinor, "OHZ017"), false, 0},
		{"excluded event", home, at("Special Weather Statement", alert.SeverityModerate, "OHZ017"), false, 0},
		{"urgent bypasses severity", home, at("Tornado Warning", alert.SeverityUnknown, "OHC063"), true, PriorityUrgent},
		{"extreme is urgent", home, at("Extreme Cold Warning", alert.SeverityExtreme, "OHZ017"), true, PriorityUrgent},
		{
			"allowlist excludes others",
			Subscriber{Zones: []string{"OHZ017"}, Events: []string{"Winter Storm Warning"}},
			at("Wind Advisory", alert.SeverityModerate, "OHZ017"), false, 0,
		},
		{
			"skip updates",
			Subscriber{Zones: []string{"OHZ017"}, SkipUpdates: true},
			alert.Alert{Event: "Wind Advisory", Severity: alert.SeverityModerate, Zones: []string{"OHZ017"}, MessageType: alert.MessageUpdate},
			false, 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.Matches(tt.alert); got != tt.match {
				t.Fatalf("Matches = %v, want %v", got, tt.match)
			}
			if tt.match {
				if got := tt.sub.Priority(tt.alert); got != tt.priority {
					t.Errorf("Priority = %d, want %d", got, tt.priority)
				}
			}
		})
	}
}
