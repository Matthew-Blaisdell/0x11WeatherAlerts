// Package notify delivers messages to phones through ntfy (https://ntfy.sh),
// either the public server or a self-hosted one.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Message struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Body     string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
}

type Notifier interface {
	Send(ctx context.Context, m Message) error
}

type Ntfy struct {
	BaseURL string // e.g. https://ntfy.sh
	Token   string // optional access token for protected topics
	HTTP    *http.Client
}

// Send publishes m using ntfy's JSON API, which handles non-ASCII text
// correctly (header-based publishing does not).
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.BaseURL, "/"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	hc := n.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
