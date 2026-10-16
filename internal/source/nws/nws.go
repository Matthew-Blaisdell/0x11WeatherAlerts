// Package nws polls the National Weather Service public alerts API
// (https://api.weather.gov). No registration is required, only a User-Agent
// that identifies the application and a contact address.
package nws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/alert"
)

const DefaultBaseURL = "https://api.weather.gov"

// Client fetches active alerts for a set of states/marine areas in a single
// request, no matter how many subscribers are filtering on them.
type Client struct {
	BaseURL   string
	UserAgent string
	Areas     []string // two-letter state or marine area codes, e.g. "OH"
	HTTP      *http.Client
}

func (c *Client) Name() string { return "nws-api" }

// Fetch returns all currently active, actual alerts for the configured areas.
func (c *Client) Fetch(ctx context.Context) ([]alert.Alert, error) {
	q := url.Values{"status": {"actual"}}
	if len(c.Areas) > 0 {
		q.Set("area", strings.Join(c.Areas, ","))
	}
	var fc featureCollection
	if err := c.get(ctx, "/alerts/active?"+q.Encode(), &fc); err != nil {
		return nil, err
	}
	alerts := make([]alert.Alert, 0, len(fc.Features))
	for _, f := range fc.Features {
		alerts = append(alerts, f.Properties.toAlert())
	}
	return alerts, nil
}

// PointZones resolves a latitude/longitude to the forecast zone and county
// zone codes used to filter alerts, e.g. ("OHZ017", "OHC063").
func (c *Client) PointZones(ctx context.Context, lat, lon float64) (place, forecastZone, countyZone string, err error) {
	var p struct {
		Properties struct {
			ForecastZone     string `json:"forecastZone"`
			County           string `json:"county"`
			RelativeLocation struct {
				Properties struct {
					City  string `json:"city"`
					State string `json:"state"`
				} `json:"properties"`
			} `json:"relativeLocation"`
		} `json:"properties"`
	}
	if err = c.get(ctx, fmt.Sprintf("/points/%.4f,%.4f", lat, lon), &p); err != nil {
		return "", "", "", err
	}
	rl := p.Properties.RelativeLocation.Properties
	return rl.City + ", " + rl.State, lastSegment(p.Properties.ForecastZone), lastSegment(p.Properties.County), nil
}

// StatusError is returned for non-200 responses so callers can back off on
// 429/5xx.
type StatusError struct {
	Code       int
	RetryAfter time.Duration
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("nws api: HTTP %d: %s", e.Code, e.Body)
}

// Temporary reports whether the request should be retried after backing off.
func (e *StatusError) Temporary() (bool, time.Duration) {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500, e.RetryAfter
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	if c.UserAgent == "" {
		return errors.New("nws api: UserAgent is required by api.weather.gov")
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/geo+json")

	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("nws api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		se := &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
		if secs, err := time.ParseDuration(resp.Header.Get("Retry-After") + "s"); err == nil {
			se.RetryAfter = secs
		}
		return se
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("nws api: decoding %s: %w", path, err)
	}
	return nil
}

func lastSegment(u string) string {
	if i := strings.LastIndexByte(u, '/'); i >= 0 {
		return u[i+1:]
	}
	return u
}

// Wire format (GeoJSON FeatureCollection of CAP alerts).

type featureCollection struct {
	Features []struct {
		Properties properties `json:"properties"`
	} `json:"features"`
}

type properties struct {
	AtID     string `json:"@id"`
	ID       string `json:"id"`
	AreaDesc string `json:"areaDesc"`
	Geocode  struct {
		UGC []string `json:"UGC"`
	} `json:"geocode"`
	References []struct {
		Identifier string `json:"identifier"`
	} `json:"references"`
	Sent        time.Time  `json:"sent"`
	Expires     time.Time  `json:"expires"`
	Ends        *time.Time `json:"ends"`
	Status      string     `json:"status"`
	MessageType string     `json:"messageType"`
	Severity    string     `json:"severity"`
	Certainty   string     `json:"certainty"`
	Urgency     string     `json:"urgency"`
	Event       string     `json:"event"`
	SenderName  string     `json:"senderName"`
	Headline    string     `json:"headline"`
	Description string     `json:"description"`
	Instruction *string    `json:"instruction"`
	Parameters  struct {
		NWSHeadline []string `json:"NWSheadline"`
	} `json:"parameters"`
}

func (p properties) toAlert() alert.Alert {
	a := alert.Alert{
		ID:          p.ID,
		MessageType: alert.MessageType(p.MessageType),
		Status:      p.Status,
		Event:       p.Event,
		Headline:    p.Headline,
		Description: p.Description,
		AreaDesc:    p.AreaDesc,
		SenderName:  p.SenderName,
		URL:         p.AtID,
		Severity:    alert.ParseSeverity(p.Severity),
		Urgency:     p.Urgency,
		Certainty:   p.Certainty,
		Zones:       p.Geocode.UGC,
		Sent:        p.Sent,
		Expires:     p.Expires,
	}
	// NWSheadline is the short, human-written summary; prefer it over the
	// generic "X issued at Y by Z" headline.
	if len(p.Parameters.NWSHeadline) > 0 && p.Parameters.NWSHeadline[0] != "" {
		a.Headline = p.Parameters.NWSHeadline[0]
	}
	// "expires" is when this message stops being current; "ends" is when the
	// hazard itself ends. Keep tracking until the later of the two.
	if p.Ends != nil && p.Ends.After(a.Expires) {
		a.Expires = *p.Ends
	}
	if p.Instruction != nil {
		a.Instruction = *p.Instruction
	}
	for _, r := range p.References {
		a.References = append(a.References, r.Identifier)
	}
	return a
}
