package nws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Matthew-Blaisdell/0x11WeatherAlerts/internal/alert"
)

// testdata/alerts_active.json is a trimmed real response from
// /alerts/active captured 2026-10-15.
func TestFetchParsesRealResponse(t *testing.T) {
	body, err := os.ReadFile("../../../testdata/alerts_active.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "(test, me@example.com)" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.URL.Query().Get("area"); got != "OH,IN" {
			t.Errorf("area = %q, want OH,IN", got)
		}
		if got := r.URL.Query().Get("status"); got != "actual" {
			t.Errorf("status = %q, want actual", got)
		}
		w.Header().Set("Content-Type", "application/geo+json")
		w.Write(body)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, UserAgent: "(test, me@example.com)", Areas: []string{"OH", "IN"}}
	alerts, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 3 {
		t.Fatalf("got %d alerts, want 3", len(alerts))
	}

	flood := alerts[0]
	if flood.Event != "Flood Advisory" || flood.Severity != alert.SeverityMinor || flood.MessageType != alert.MessageAlert {
		t.Errorf("flood advisory parsed as %+v", flood)
	}
	if len(flood.Zones) == 0 || flood.Zones[0] != "INC055" {
		t.Errorf("zones = %v, want county code INC055 first", flood.Zones)
	}

	wind := alerts[1]
	if wind.MessageType != alert.MessageUpdate || len(wind.References) == 0 {
		t.Errorf("update should carry references to the superseded alert: %+v", wind)
	}

	sps := alerts[2]
	if sps.Headline == "" || sps.Headline[:7] == "Special" {
		t.Errorf("headline should prefer NWSheadline over the generic one, got %q", sps.Headline)
	}
	if !sps.Actual() || sps.Expires.IsZero() || sps.Instruction == "" {
		t.Errorf("missing fields: %+v", sps)
	}
}

func TestFetchStatusErrorIsTemporaryOn5xxAnd429(t *testing.T) {
	for code, want := range map[int]bool{429: true, 503: true, 404: false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "90")
			http.Error(w, "nope", code)
		}))
		_, err := (&Client{BaseURL: srv.URL, UserAgent: "x"}).Fetch(context.Background())
		srv.Close()

		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("HTTP %d: want *StatusError, got %v", code, err)
		}
		retry, after := se.Temporary()
		if retry != want || after != 90*time.Second {
			t.Errorf("HTTP %d: Temporary() = %v, %s; want %v, 90s", code, retry, after, want)
		}
	}
}

func TestFetchRequiresUserAgent(t *testing.T) {
	if _, err := (&Client{BaseURL: "http://unused"}).Fetch(context.Background()); err == nil {
		t.Fatal("expected an error without a User-Agent")
	}
}
