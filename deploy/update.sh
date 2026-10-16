#!/bin/sh
# Nightly self-update, run from cron on the host:
#   30 4 * * * $HOME/0x11WeatherAlerts/deploy/update.sh >> $HOME/0x11WeatherAlerts-update.log 2>&1
#
# Pulls main, rebuilds with fresh base images (picks up Go patch releases),
# and recreates the container only if the image actually changed. Any failure
# leaves the running version in place and is reported to the ntfy topic.
set -u
cd "$(dirname "$0")/.." || exit 1

log() { echo "$(date -Is) $*"; }

fail() {
	log "FAILED: $1"
	topic=$(sed -n 's/^ *topic: *\([^ #]*\).*/\1/p' config.yaml | head -n 1)
	server=$(sed -n 's/^ *url: *\([^ #]*\).*/\1/p' config.yaml | head -n 1)
	if [ -n "$topic" ]; then
		curl -fsS -m 20 -o /dev/null \
			-H "Title: 0x11WeatherAlerts update failed" -H "Priority: 3" -H "Tags: warning" \
			-d "Nightly update on $(hostname) failed at: $1. See ~/0x11WeatherAlerts-update.log." \
			"${server:-https://ntfy.sh}/$topic" || log "could not send failure notification"
	fi
	exit 1
}

before=$(git rev-parse --short HEAD)
git pull --ff-only -q || fail "git pull"
docker compose build --pull --quiet || fail "docker compose build"
docker compose up -d --quiet-pull 2>/dev/null || docker compose up -d || fail "docker compose up"
docker image prune -f >/dev/null

# Give a recreated container time to complete its first poll.
sleep 90
curl -fsS -m 10 -o /dev/null http://127.0.0.1:8080/healthz || fail "health check after update"
log "ok $before -> $(git rev-parse --short HEAD)"
