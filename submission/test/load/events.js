import http from 'k6/http';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';

const baseURL = __ENV.SERVICE_URL || 'http://localhost:8090';
const runID = __ENV.RUN_ID || 'manual';
const deviceCount = Number(__ENV.DEVICES || 5000);
const baselineRate = Number(__ENV.BASELINE_RATE || 5000);
const burstRate = Number(__ENV.BURST_RATE || 50000);
const extraBurstRate = burstRate - baselineRate;
const preAllocatedVUs = Number(__ENV.PRE_ALLOCATED_VUS || 500);
const maxVUs = Number(__ENV.MAX_VUS || 10000);
const baselineOnly = __ENV.BASELINE_ONLY === '1';

if (deviceCount < 1 || baselineRate < 1 || (!baselineOnly && extraBurstRate < 1)) {
  throw new Error('DEVICES, BASELINE_RATE, and BURST_RATE must be positive; BURST_RATE must exceed BASELINE_RATE');
}

const accepted = new Counter('events_accepted');
const duplicates = new Counter('events_duplicate');
const overloaded = new Counter('events_overloaded');
const unexpected = new Counter('events_unexpected');
const transportErrors = new Counter('events_transport_errors');
const clientErrors = new Counter('events_client_errors');
const serverErrors = new Counter('events_server_errors');
const otherErrors = new Counter('events_other_errors');

http.setResponseCallback(http.expectedStatuses({ min: 200, max: 299 }, 503));

const scenarios = {
  baseline: {
    executor: 'constant-arrival-rate',
    exec: 'sendEvent',
    rate: baselineRate,
    timeUnit: '1s',
    duration: __ENV.BASELINE_DURATION || '3m',
    preAllocatedVUs,
    maxVUs,
    gracefulStop: '5s',
  },
};

const thresholds = {
  http_req_failed: ['rate==0'],
  events_unexpected: ['count==0'],
  'events_accepted{scenario:baseline}': ['count>=0'],
  'events_overloaded{scenario:baseline}': ['count>=0'],
  'http_reqs{scenario:baseline}': ['count>0'],
  'http_req_duration{scenario:baseline}': ['p(95)<60000'],
};

if (!baselineOnly) {
  Object.assign(scenarios, {
    burst_one: {
      executor: 'constant-arrival-rate',
      exec: 'sendEvent',
      startTime: __ENV.FIRST_BURST_START || '1m',
      rate: extraBurstRate,
      timeUnit: '1s',
      duration: __ENV.BURST_DURATION || '30s',
      preAllocatedVUs,
      maxVUs,
      gracefulStop: '5s',
    },
    burst_two: {
      executor: 'constant-arrival-rate',
      exec: 'sendEvent',
      startTime: __ENV.SECOND_BURST_START || '2m',
      rate: extraBurstRate,
      timeUnit: '1s',
      duration: __ENV.BURST_DURATION || '30s',
      preAllocatedVUs,
      maxVUs,
      gracefulStop: '5s',
    },
  });
  Object.assign(thresholds, {
    'events_accepted{scenario:burst_one}': ['count>=0'],
    'events_accepted{scenario:burst_two}': ['count>=0'],
    'events_overloaded{scenario:burst_one}': ['count>=0'],
    'events_overloaded{scenario:burst_two}': ['count>=0'],
    'http_reqs{scenario:burst_one}': ['count>0'],
    'http_reqs{scenario:burst_two}': ['count>0'],
    'http_req_duration{scenario:burst_one}': ['p(95)<60000'],
    'http_req_duration{scenario:burst_two}': ['p(95)<60000'],
  });
}

export const options = {
  discardResponseBodies: true,
  scenarios,
  thresholds,
};

const sequenceOffsets = {
  baseline: 0,
  burst_one: 1000000000,
  burst_two: 2000000000,
};

export function sendEvent() {
  const iteration = exec.scenario.iterationInTest;
  const deviceNumber = iteration % deviceCount;
  const sequence = sequenceOffsets[exec.scenario.name] + Math.floor(iteration / deviceCount) + 1;
  const event = {
    device_id: `load_${runID}_dev_${deviceNumber}`,
    room_id: `load_${runID}_room_${Math.floor(deviceNumber / 2)}`,
    type: eventType(iteration),
    ts: new Date().toISOString(),
    seq: sequence,
  };

  switch (event.type) {
    case 'presence':
      event.in_room = sequence % 2 === 0;
      break;
    case 'fall_warn':
      event.confidence = 0.92;
      break;
  }

  const response = http.post(`${baseURL}/events`, JSON.stringify(event), {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'POST /events', event_type: event.type },
    timeout: '5s',
  });

  switch (response.status) {
    case 202:
      accepted.add(1);
      break;
    case 200:
      duplicates.add(1);
      break;
    case 503:
      overloaded.add(1);
      break;
    default:
      unexpected.add(1);
      recordUnexpected(response.status);
  }
}

function recordUnexpected(status) {
  if (status === 0) {
    transportErrors.add(1);
    return;
  }
  if (status >= 400 && status < 500) {
    clientErrors.add(1);
    return;
  }
  if (status >= 500) {
    serverErrors.add(1);
    return;
  }
  otherErrors.add(1);
}

function eventType(iteration) {
  if (iteration % 1000 === 0) {
    return 'fall_warn';
  }
  if (iteration % 25 === 0) {
    return 'presence';
  }
  return 'heartbeat';
}
