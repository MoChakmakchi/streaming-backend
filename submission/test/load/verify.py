#!/usr/bin/env python3
import argparse
import json
import math
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


def timestamp(value: datetime) -> str:
    return value.isoformat(timespec="milliseconds").replace("+00:00", "Z")


def parse_timestamp(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


class Client:
    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")

    def get(self, path: str) -> dict:
        status, body = self._request("GET", path)
        require(status == 200, f"GET {path} returned {status}: {body}")
        return body

    def post_event(self, event: dict, on_attempt=None) -> dict:
        status, body = self._request("POST", "/events", event, True, on_attempt)
        require(status in (200, 202), f"POST /events returned {status}: {body}")
        return body

    def _request(self, method, path, payload=None, retry=False, on_attempt=None):
        deadline = time.monotonic() + 30
        data = json.dumps(payload).encode() if payload is not None else None
        headers = {"Content-Type": "application/json"} if data is not None else {}

        while True:
            if on_attempt is not None:
                on_attempt(time.monotonic())
            request = Request(self.base_url + path, data=data, headers=headers, method=method)
            try:
                with urlopen(request, timeout=5) as response:
                    return response.status, json.loads(response.read())
            except HTTPError as error:
                body = json.loads(error.read() or b"{}")
                if error.code != 503 or not retry or time.monotonic() >= deadline:
                    return error.code, body
                time.sleep(float(error.headers.get("Retry-After", "1")))
            except URLError:
                if not retry or time.monotonic() >= deadline:
                    raise
                time.sleep(0.1)


class AlarmCollector:
    def __init__(self, base_url, device_prefix, expected, sent_at, sent_lock):
        self.url = base_url.rstrip("/") + "/alarms/stream"
        self.device_prefix = device_prefix
        self.expected = expected
        self.sent_at = sent_at
        self.sent_lock = sent_lock
        self.ready = threading.Event()
        self.alarms = []
        self.error = None
        self.thread = threading.Thread(target=self._run, daemon=True)

    def start(self):
        self.thread.start()
        require(self.ready.wait(5), "SSE stream did not open")
        if self.error is not None:
            raise self.error

    def wait(self):
        self.thread.join(30)
        require(not self.thread.is_alive(), "timed out waiting for probe alarms")
        if self.error is not None:
            raise self.error
        require(len(self.alarms) == self.expected, f"received {len(self.alarms)} probe alarms, expected {self.expected}")

    def _run(self):
        try:
            with urlopen(self.url, timeout=30) as response:
                self.ready.set()
                data = []
                for raw_line in response:
                    line = raw_line.decode().rstrip("\r\n")
                    if line.startswith("data: "):
                        data.append(line[6:])
                    if line != "" or not data:
                        continue

                    alarm = json.loads("\n".join(data))
                    data = []
                    if not alarm["device_id"].startswith(self.device_prefix):
                        continue

                    key = (alarm["device_id"], parse_timestamp(alarm["ts"]))
                    with self.sent_lock:
                        started_at = self.sent_at.get(key)
                    if started_at is None:
                        continue
                    alarm["delivery_seconds"] = time.monotonic() - started_at
                    self.alarms.append(alarm)
                    if len(self.alarms) == self.expected:
                        return
        except Exception as error:  # surfaced to the main thread
            self.error = error
            self.ready.set()


def verify_health(client: Client, prefix: str) -> None:
    device_id = prefix + "_device_5001"
    room_id = prefix + "_health_room"
    now = datetime.now(timezone.utc)
    event_times = [now - timedelta(seconds=2), now - timedelta(seconds=4), now - timedelta(seconds=3)]

    for sequence, event_time in enumerate(event_times, 1):
        client.post_event({
            "device_id": device_id,
            "room_id": room_id,
            "type": "heartbeat",
            "ts": timestamp(event_time),
            "seq": sequence,
        })

    health = client.get(f"/devices/{device_id}/health")
    actual = parse_timestamp(health["last_heartbeat_ts"])
    expected = parse_timestamp(timestamp(event_times[0]))
    require(actual == expected, f"latest heartbeat was {actual.isoformat()}, expected {expected.isoformat()}")
    require(abs(health["availability_5m"] - 0.01) < 0.000001, f"unexpected availability: {health}")


def verify_occupancy(client: Client, prefix: str) -> None:
    device_id = prefix + "_occupancy_device"
    room_id = prefix + "_occupancy_room"
    now = datetime.now(timezone.utc)
    oldest = now - timedelta(seconds=50)
    late = now - timedelta(seconds=30)
    newest = now - timedelta(seconds=20)

    for sequence, event_time in ((1, newest), (2, oldest)):
        client.post_event({
            "device_id": device_id,
            "room_id": room_id,
            "type": "presence",
            "ts": timestamp(event_time),
            "seq": sequence,
            "in_room": True,
        })

    before_time = datetime.now(timezone.utc)
    before = client.get(f"/rooms/{room_id}/occupancy?window=1m")
    expected_before = (before_time - oldest).total_seconds() / 60
    require(before["in_room"], "newest presence state was not used")
    require(abs(before["occupied_pct"] - expected_before) < 0.03, f"unexpected initial occupancy: {before}")

    client.post_event({
        "device_id": device_id,
        "room_id": room_id,
        "type": "presence",
        "ts": timestamp(late),
        "seq": 3,
        "in_room": False,
    })
    after_time = datetime.now(timezone.utc)
    after = client.get(f"/rooms/{room_id}/occupancy?window=1m")
    expected_after = (late - oldest).total_seconds() / 60 + (after_time - newest).total_seconds() / 60
    require(after["in_room"], "late presence event replaced the newer current state")
    require(abs(after["occupied_pct"] - expected_after) < 0.03, f"late event did not correct occupancy: {after}")


def verify_alarms(client: Client, prefix: str, sample_count: int) -> float:
    sent_at = {}
    sent_lock = threading.Lock()
    collector = AlarmCollector(client.base_url, prefix, sample_count + 2, sent_at, sent_lock)
    collector.start()

    def send_fall(device_id, room_id, sequence, event_time):
        event_ts = timestamp(event_time)

        def record_start(started_at):
            with sent_lock:
                sent_at[(device_id, parse_timestamp(event_ts))] = started_at

        client.post_event({
            "device_id": device_id,
            "room_id": room_id,
            "type": "fall_warn",
            "ts": event_ts,
            "seq": sequence,
            "confidence": 0.92,
        }, record_start)

    base_time = datetime.now(timezone.utc)
    dedupe_device = prefix + "_dedupe_device"
    dedupe_room = prefix + "_dedupe_room"
    send_fall(dedupe_device, dedupe_room, 1, base_time)
    send_fall(dedupe_device, dedupe_room, 2, base_time + timedelta(seconds=2))
    send_fall(dedupe_device, dedupe_room, 3, base_time + timedelta(seconds=4))

    with ThreadPoolExecutor(max_workers=min(sample_count, 10)) as executor:
        futures = [
            executor.submit(
                send_fall,
                f"{prefix}_latency_device_{number}",
                f"{prefix}_latency_room_{number}",
                1,
                base_time,
            )
            for number in range(sample_count)
        ]
        for future in futures:
            future.result()

    collector.wait()
    dedupe = [alarm for alarm in collector.alarms if alarm["device_id"] == dedupe_device]
    actual_times = [parse_timestamp(alarm["ts"]) for alarm in dedupe]
    expected_times = [parse_timestamp(timestamp(base_time)), parse_timestamp(timestamp(base_time + timedelta(seconds=4)))]
    require(actual_times == expected_times,
            "fall deduplication or per-room SSE order was incorrect")

    latencies = sorted(alarm["delivery_seconds"] for alarm in collector.alarms)
    p95 = latencies[math.ceil(len(latencies) * 0.95) - 1]
    require(p95 <= 1, f"alarm delivery p95 was {p95:.3f}s, expected at most 1s")

    since = collector.alarms[0]["created_at"]
    history = client.get("/alarms?" + urlencode({"since": since}))["alarms"]
    dedupe_history = [alarm for alarm in history if alarm["device_id"] == dedupe_device]
    require(len(dedupe_history) == 2, f"alarm history contained {len(dedupe_history)} dedupe alarms, expected 2")
    require(dedupe_history[0]["event_id"] == dedupe[0]["event_id"], "inclusive alarm history omitted its boundary")

    missed_device = prefix + "_missed_device"
    send_fall(missed_device, prefix + "_missed_room", 1, datetime.now(timezone.utc))
    recovered = client.get("/alarms?" + urlencode({"since": since}))["alarms"]
    require(any(alarm["device_id"] == missed_device for alarm in recovered), "history did not recover an alarm sent after SSE disconnect")
    return p95


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", default="http://localhost:8090")
    parser.add_argument("--delay", type=float, default=0, help="seconds to wait before probing")
    parser.add_argument("--alarm-samples", type=int, default=20)
    args = parser.parse_args()

    require(args.alarm_samples > 0, "--alarm-samples must be positive")
    if args.delay > 0:
        print(f"waiting {args.delay:g}s before probing")
        time.sleep(args.delay)

    prefix = f"probe_{time.time_ns()}"
    client = Client(args.target)
    verify_health(client, prefix)
    verify_occupancy(client, prefix)
    alarm_p95 = verify_alarms(client, prefix, args.alarm_samples)
    print(json.dumps({
        "status": "passed",
        "probe": prefix,
        "alarm_delivery_p95_ms": round(alarm_p95 * 1000, 3),
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"verification failed: {error}", file=sys.stderr)
        sys.exit(1)
