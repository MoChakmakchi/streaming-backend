#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

BASE_URL="${SERVICE_URL:-http://localhost:8090}"
COMPOSE=(docker compose -f deployment/compose.yaml)
RUN_ID="$(date +%s)"
DEVICE_ID="restart_device_${RUN_ID}"
ROOM_ID="restart_room_${RUN_ID}"
EVENT_TIME="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

post_event() {
	local body="$1"
	local expected_status="$2"
	local response_file="$3"
	local status

	status="$(curl -sS -o "$response_file" -w '%{http_code}' \
		-X POST "$BASE_URL/events" \
		-H 'Content-Type: application/json' \
		-d "$body")"
	if [[ "$status" != "$expected_status" ]]; then
		echo "POST /events returned $status, expected $expected_status" >&2
		cat "$response_file" >&2
		exit 1
	fi
}

wait_for_api() {
	for _ in {1..30}; do
		if curl -fsS "$BASE_URL/metrics" >/dev/null; then
			return
		fi
		sleep 1
	done
	echo "API did not become ready" >&2
	exit 1
}

HEARTBEAT="{\"device_id\":\"$DEVICE_ID\",\"room_id\":\"$ROOM_ID\",\"type\":\"heartbeat\",\"ts\":\"$EVENT_TIME\",\"seq\":1}"
PRESENCE="{\"device_id\":\"$DEVICE_ID\",\"room_id\":\"$ROOM_ID\",\"type\":\"presence\",\"ts\":\"$EVENT_TIME\",\"seq\":2,\"in_room\":true}"
FALL="{\"device_id\":\"$DEVICE_ID\",\"room_id\":\"$ROOM_ID\",\"type\":\"fall_warn\",\"ts\":\"$EVENT_TIME\",\"seq\":3,\"confidence\":0.92}"

post_event "$HEARTBEAT" 202 "$TMP_DIR/heartbeat-before.json"
post_event "$PRESENCE" 202 "$TMP_DIR/presence-before.json"
post_event "$FALL" 202 "$TMP_DIR/fall-before.json"

curl -fsS "$BASE_URL/devices/$DEVICE_ID/health" >"$TMP_DIR/health-before.json"
curl -fsS "$BASE_URL/rooms/$ROOM_ID/occupancy?window=1m" >"$TMP_DIR/occupancy-before.json"
curl -fsS "$BASE_URL/alarms?since=0" >"$TMP_DIR/alarms-before.json"

grep -Fq "\"last_heartbeat_ts\":\"$EVENT_TIME\"" "$TMP_DIR/health-before.json"
grep -Fq '"in_room":true' "$TMP_DIR/occupancy-before.json"
grep -Fq "\"device_id\":\"$DEVICE_ID\"" "$TMP_DIR/alarms-before.json"

"${COMPOSE[@]}" kill -s SIGKILL api
make migrate
"${COMPOSE[@]}" up -d api
wait_for_api

curl -fsS "$BASE_URL/devices/$DEVICE_ID/health" >"$TMP_DIR/health-after.json"
curl -fsS "$BASE_URL/rooms/$ROOM_ID/occupancy?window=1m" >"$TMP_DIR/occupancy-after.json"
curl -fsS "$BASE_URL/alarms?since=0" >"$TMP_DIR/alarms-after.json"

grep -Fq "\"last_heartbeat_ts\":\"$EVENT_TIME\"" "$TMP_DIR/health-after.json"
grep -Fq '"in_room":true' "$TMP_DIR/occupancy-after.json"
grep -Fq "\"device_id\":\"$DEVICE_ID\"" "$TMP_DIR/alarms-after.json"

post_event "$HEARTBEAT" 200 "$TMP_DIR/heartbeat-retry.json"
post_event "$PRESENCE" 200 "$TMP_DIR/presence-retry.json"
post_event "$FALL" 200 "$TMP_DIR/fall-retry.json"
grep -Fq '"status":"duplicate"' "$TMP_DIR/heartbeat-retry.json"
grep -Fq '"status":"duplicate"' "$TMP_DIR/presence-retry.json"
grep -Fq '"status":"duplicate"' "$TMP_DIR/fall-retry.json"

EVENT_COUNT="$("${COMPOSE[@]}" exec -T postgres psql -U teton -d teton -Atc \
	"SELECT count(*) FROM events WHERE device_id = '$DEVICE_ID'")"
ALARM_COUNT="$("${COMPOSE[@]}" exec -T postgres psql -U teton -d teton -Atc \
	"SELECT count(*) FROM alarms WHERE device_id = '$DEVICE_ID'")"
if [[ "$EVENT_COUNT" != "3" || "$ALARM_COUNT" != "1" ]]; then
	echo "unexpected durable counts: events=$EVENT_COUNT alarms=$ALARM_COUNT" >&2
	exit 1
fi

echo "restart verification passed: events=$EVENT_COUNT alarms=$ALARM_COUNT"
