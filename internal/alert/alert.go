// Package alert defines the source-independent alert model. Every data source
// (the NWS REST API today, NWWS-OI later) converts its payloads into Alert.
package alert

import (
	"strings"
	"time"
)

// Severity follows the CAP severity scale, ordered so comparisons work.
type Severity int

const (
	SeverityUnknown Severity = iota
	SeverityMinor
	SeverityModerate
	SeveritySevere
	SeverityExtreme
)

// ParseSeverity accepts CAP severity strings case-insensitively.
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "minor":
		return SeverityMinor
	case "moderate":
		return SeverityModerate
	case "severe":
		return SeveritySevere
	case "extreme":
		return SeverityExtreme
	default:
		return SeverityUnknown
	}
}

func (s Severity) String() string {
	return [...]string{"Unknown", "Minor", "Moderate", "Severe", "Extreme"}[s]
}

// MessageType is the CAP msgType: a new alert, an update to an earlier one,
// or a cancellation.
type MessageType string

const (
	MessageAlert  MessageType = "Alert"
	MessageUpdate MessageType = "Update"
	MessageCancel MessageType = "Cancel"
)

type Alert struct {
	// ID is globally unique per message. An Update gets a new ID and lists
	// the messages it supersedes in References.
	ID          string
	References  []string
	MessageType MessageType
	Status      string // Actual, Exercise, System, Test, Draft

	Event       string // e.g. "Tornado Warning"
	Headline    string
	Description string
	Instruction string
	AreaDesc    string
	SenderName  string
	URL         string

	Severity  Severity
	Urgency   string
	Certainty string

	// UGC codes for the affected area: county (OHC063) or forecast zone (OHZ017).
	Zones []string

	Sent    time.Time
	Expires time.Time
}

// Actual reports whether this is a real-world alert rather than a test or exercise.
func (a Alert) Actual() bool { return strings.EqualFold(a.Status, "Actual") }

// Expired reports whether the alert is no longer in effect at now.
// An alert without an expiry time never expires on its own.
func (a Alert) Expired(now time.Time) bool {
	return !a.Expires.IsZero() && now.After(a.Expires)
}
