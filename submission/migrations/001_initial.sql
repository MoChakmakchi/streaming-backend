CREATE TABLE IF NOT EXISTS events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    device_id text NOT NULL CHECK (device_id <> ''),
    room_id text NOT NULL CHECK (room_id <> ''),
    event_type text NOT NULL CHECK (
        event_type IN ('heartbeat', 'presence', 'motion', 'sleep_state', 'fall_warn', 'net_status')
    ),
    event_time timestamptz NOT NULL,
    seq bigint NOT NULL CHECK (seq >= 0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    received_at timestamptz NOT NULL,
    UNIQUE (device_id, seq)
);

CREATE INDEX IF NOT EXISTS events_heartbeat_device_time_idx
    ON events (device_id, event_time, seq)
    WHERE event_type = 'heartbeat';

CREATE INDEX IF NOT EXISTS events_presence_room_time_idx
    ON events (room_id, event_time, device_id, seq)
    WHERE event_type = 'presence';

CREATE TABLE IF NOT EXISTS device_health (
    device_id text PRIMARY KEY,
    room_id text NOT NULL,
    event_id bigint NOT NULL REFERENCES events (id),
    last_heartbeat_at timestamptz NOT NULL,
    last_seq bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS room_occupancy (
    room_id text PRIMARY KEY,
    in_room boolean NOT NULL,
    event_id bigint NOT NULL REFERENCES events (id),
    last_event_at timestamptz NOT NULL,
    last_device_id text NOT NULL,
    last_seq bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS alarms (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_event_id bigint NOT NULL UNIQUE REFERENCES events (id),
    device_id text NOT NULL CHECK (device_id <> ''),
    room_id text NOT NULL CHECK (room_id <> ''),
    event_time timestamptz NOT NULL,
    confidence double precision NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS alarms_device_room_time_idx
    ON alarms (device_id, room_id, event_time);

CREATE INDEX IF NOT EXISTS alarms_created_idx
    ON alarms (created_at, id);

CREATE INDEX IF NOT EXISTS alarms_room_created_idx
    ON alarms (room_id, created_at, id);
