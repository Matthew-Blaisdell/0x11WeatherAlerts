# 0x11WeatherAlerts

Severe weather alerts from the National Weather Service, pushed to your phone
through [ntfy](https://ntfy.sh) within about a minute of issuance, at a
priority that **shows on the lock screen and bypasses Do Not Disturb** for the
alerts that matter (tornado, severe thunderstorm, flash flood warnings).

I built it because my phone's weather app only surfaced warnings after I
unlocked the phone.

## How it works

```
api.weather.gov ──poll──▶ Source ──▶ Dispatcher ──match──▶ Subscriber filters
  (1 request per                         │                         │
   interval for all          SQLite: delivered(alert, subscriber)  ▼
   subscribers)                          └────────────────────▶ ntfy ──▶ phone
```

- **Source**: fetches active alerts for a list of states in a single request.
  Behind an interface so a push-based NWWS-OI (XMPP) source can replace polling
  later without touching anything else.
- **Subscribers**: each has its own zones, minimum severity, event allow/deny
  lists, and "urgent" events that bypass every filter and get ntfy priority 5.
- **Dispatcher**: delivers each `(alert, subscriber)` pair exactly once. The
  delivery record is written only after ntfy accepts the message, so a failed
  send is retried on the next poll, and a crash can cause at most one
  duplicate, never a missed warning.
- **Updates and cancellations**: NWS issues a new message ID when it updates
  an alert. Updates are delivered as "Updated: …" (optional per subscriber);
  cancellations are only sent to subscribers who received the original.

## Being a polite API client

- One request per interval covers every subscriber and area. Adding
  subscribers adds zero requests.
- The interval can't go below 30s; the API caches responses for a few seconds
  anyway, and the default is 60s (1,440 requests/day).
- On HTTP 429 or 5xx the poller backs off exponentially, up to 10 minutes,
  and honors `Retry-After`.
- Sends the identifying `User-Agent` with contact info that NWS requires.

## Usage

Find the zone codes for your location:

```console
$ weatheralerts lookup 41.0442,-83.6499
Findlay, OH
  areas: [OH]
  zones: [OHC063, OHZ017]
```

List both codes: convective warnings (tornado, thunderstorm, flash flood) are
issued by county (`C`), while wind, winter, and heat products use forecast
zones (`Z`).

```sh
cp config.example.yaml config.yaml   # set contact, zones, ntfy topic
docker compose up -d --build
curl localhost:8080/healthz          # 200 while polls are succeeding
curl localhost:8080/metrics          # Prometheus text format
```

On the phone, install the ntfy app and subscribe to your topic. Enable
"instant delivery" so notifications arrive without polling delay.

## Development

```sh
nix develop          # go, gopls, golangci-lint, govulncheck, sqlite
go test ./...
govulncheck ./...    # also runs in CI, weekly
```

`testdata/alerts_active.json` is a trimmed real API response used by the
parser tests; the dispatcher tests run against a real SQLite store.

## Roadmap

- NWWS-OI source (XMPP push, registration pending) for second-level latency
- Self-hosted ntfy behind a Cloudflare Tunnel
- Link notifications to a human-readable alert page instead of the API JSON
- Polygon-based matching for storm-based warnings, not just county codes
