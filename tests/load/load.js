import http from 'k6/http';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';
import { check, sleep, fail } from 'k6';
import { Counter } from 'k6/metrics';

const BASE = (__ENV.BASE_URL || 'https://dashboard.dev.eksv2.naimaosmann.com').replace(/\/+$/, '');
const SECRET = __ENV.JWT_SECRET || '';
const TEST = __ENV.TEST || '';
const RUN = __ENV.RUN_ID || '';
const RAMP = __ENV.RAMP || '2m';
const HOLD = __ENV.HOLD || '5m';
const DOWN = __ENV.DOWN || '1m';
const TABS = Number(__ENV.TABS || 3);
if (!(TABS >= 1 && TABS <= 4)) {
  throw new Error('TABS must be 1 to 4: each open dashboard tab sends 7 health checks per 30 s and anonymous traffic is limited to 60 a minute per IP');
}

if (!/^[a-z0-9]{1,20}$/.test(RUN)) {
  throw new Error('Set -e RUN_ID=<letters and digits only, max 20, new for every run>, for example l1a');
}
if (!SECRET) {
  throw new Error('Set the JWT_SECRET environment variable (AWS secret eks-v2-dev-jwt-secret)');
}
if (!/\.dev\.|\/\/localhost|\/\/127\.0\.0\.1/.test(BASE) && __ENV.ALLOW_NON_DEV !== 'yes') {
  throw new Error('BASE_URL is not a dev address. These tests create real orders: dev or local only.');
}

const HOT = 'lt-hot';
const HOT_STOCK = 1000000;
const SPREAD_STOCK = 100000;
const POOL = 2000;
const LOOKUP_ORDERS = 20;
const ADMIN = 'admin@platform.local';

const SPREAD = [];
for (let i = 1; i <= 60; i++) {
  SPREAD.push('lt-p' + (i < 10 ? '0' + i : '' + i));
}

const MIXES = {
  normal: [['browse', 40], ['detail', 45], ['checkout', 5], ['lookup', 10]],
  peak: [['browse', 35], ['detail', 45], ['checkout', 10], ['lookup', 10]],
};

const LEVELS = {
  L1: { read95: 300, read99: 800, order95: 500, order99: 1500, fail: 0.001 },
  L2: { read95: 500, read99: 1500, order95: 1000, order99: 3000, fail: 0.005 },
};

const STAFF_PATHS = [
  '/dashboard/summary',
  '/api/orders/',
  '/api/inventory/products',
  '/dashboard/revenue',
  '/api/payments/ledger',
  '/api/shipping/shipments',
  '/dashboard/shipping/overview',
  '/api/notifications/history',
  '/healthz',
  '/api/orders/healthz',
  '/api/inventory/healthz',
  '/api/payments/healthz',
  '/api/notifications/healthz',
  '/api/shipping/healthz',
  '/dashboard/healthz',
];

const ordersCreated = new Counter('orders_created');
const rateLimited = new Counter('rate_limited');

http.setResponseCallback(http.expectedStatuses(200, 201));

function toSeconds(s) {
  const m = /^(\d+)(s|m|h)$/.exec(s);
  if (!m) {
    throw new Error('Bad duration: ' + s + ' (use 30s, 5m or 2h)');
  }
  return Number(m[1]) * { s: 1, m: 60, h: 3600 }[m[2]];
}

function thresholdsFor(level) {
  const t = LEVELS[level];
  const th = {
    checks: ['rate>0.999'],
    dropped_iterations: ['count<1'],
    http_req_failed: ['rate<' + t.fail, { threshold: 'rate<0.5', abortOnFail: true, delayAbortEval: '30s' }],
    'http_req_failed{journey:checkout}': ['rate<' + t.fail],
    'http_req_duration{journey:checkout}': ['p(95)<' + t.order95, 'p(99)<' + t.order99],
  };
  ['browse', 'detail', 'lookup'].forEach((j) => {
    th['http_req_duration{journey:' + j + '}'] = ['p(95)<' + t.read95, 'p(99)<' + t.read99];
  });
  return th;
}

function ramp(rate, mix, hotShare, hold) {
  return {
    executor: 'ramping-arrival-rate',
    startRate: 1,
    timeUnit: '1s',
    preAllocatedVUs: Math.max(20, rate * 2),
    maxVUs: Math.max(100, rate * 6),
    stages: [
      { duration: RAMP, target: rate },
      { duration: hold, target: rate },
      { duration: DOWN, target: 0 },
    ],
    exec: 'shopper',
    env: { MIX: mix, HOT_SHARE: String(hotShare) },
  };
}

function once(execName, iterations, maxDuration) {
  return { executor: 'shared-iterations', vus: 1, iterations: iterations, maxDuration: maxDuration, exec: execName };
}

const RATE_L1 = Number(__ENV.RATE || 15);
const RATE_L2 = Number(__ENV.RATE || 150);
const TOTAL_L1 = toSeconds(RAMP) + toSeconds(HOLD) + toSeconds(DOWN);

const TESTS = {
  preflight: {
    scenarios: { preflight: once('preflight', 1, '3m') },
    thresholds: { checks: ['rate==1'] },
  },
  l1: {
    scenarios: { l1: ramp(RATE_L1, 'normal', 0, HOLD) },
    thresholds: thresholdsFor('L1'),
  },
  l2: {
    scenarios: { l2: ramp(RATE_L2, 'peak', 0, HOLD) },
    thresholds: thresholdsFor('L2'),
  },
  hot: {
    scenarios: { hot: ramp(RATE_L2, 'peak', 0.3, HOLD) },
    thresholds: thresholdsFor('L2'),
  },
  soak: {
    scenarios: { soak: ramp(RATE_L1, 'normal', 0, __ENV.HOLD || '2h') },
    thresholds: thresholdsFor('L1'),
  },
  spike: {
    scenarios: {
      spike: {
        executor: 'ramping-arrival-rate',
        startRate: 15,
        timeUnit: '1s',
        preAllocatedVUs: 200,
        maxVUs: 1000,
        stages: [
          { duration: '30s', target: 150 },
          { duration: '2m', target: 150 },
          { duration: '30s', target: 15 },
        ],
        exec: 'shopper',
        env: { MIX: 'peak', HOT_SHARE: '0.3' },
      },
      recovery: {
        executor: 'constant-arrival-rate',
        rate: 15,
        timeUnit: '1s',
        duration: '2m',
        startTime: '3m30s',
        preAllocatedVUs: 50,
        maxVUs: 200,
        exec: 'shopper',
        env: { MIX: 'normal', HOT_SHARE: '0' },
      },
    },
    thresholds: {
      checks: ['rate>0.999'],
      'http_req_failed{scenario:recovery}': ['rate<0.001'],
      'http_req_duration{scenario:recovery}': ['p(95)<500'],
    },
  },
  dashboard: {
    scenarios: {
      shoppers: ramp(RATE_L1, 'normal', 0, HOLD),
      staff: {
        executor: 'ramping-arrival-rate',
        startRate: TABS,
        timeUnit: '30s',
        preAllocatedVUs: 10,
        maxVUs: 50,
        stages: [{ duration: TOTAL_L1 + 's', target: TABS }],
        exec: 'staff',
      },
    },
    thresholds: Object.assign(thresholdsFor('L1'), {
      'http_req_failed{journey:staff}': ['rate<0.001'],
      'http_req_duration{name:staff-summary}': ['p(95)<1000'],
    }),
  },
  ratelimit: {
    scenarios: { ratelimit: once('ratelimit', 1, '5m') },
    thresholds: { checks: ['rate==1'] },
  },
  retry: {
    scenarios: { retry: once('retry', 10, '15m') },
    thresholds: { checks: ['rate==1'] },
    batch: 50,
    batchPerHost: 50,
  },
};

if (!TESTS[TEST]) {
  throw new Error('Set -e TEST=<one of: ' + Object.keys(TESTS).join(', ') + '>');
}

export const options = Object.assign(
  {
    setupTimeout: '3m',
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  },
  TESTS[TEST]
);

const tokens = {};

function sub(name) {
  return 'lt-' + RUN + '-' + name + '@example.invalid';
}

function tokenFor(who) {
  const now = Date.now();
  const hit = tokens[who];
  if (hit && now - hit.at < 40 * 60 * 1000) {
    return hit.value;
  }
  const iat = Math.floor(now / 1000);
  const header = encoding.b64encode(JSON.stringify({ alg: 'HS256', typ: 'JWT' }), 'rawurl');
  const payload = encoding.b64encode(JSON.stringify({ sub: who, role: 'customer', iat: iat, exp: iat + 3600 }), 'rawurl');
  const signature = crypto.hmac('sha256', SECRET, header + '.' + payload, 'base64rawurl');
  const value = header + '.' + payload + '.' + signature;
  tokens[who] = { value: value, at: now };
  return value;
}

function call(method, path, who, journey, body, opts) {
  const o = opts || {};
  const headers = { Authorization: 'Bearer ' + tokenFor(who), 'Content-Type': 'application/json' };
  if (o.key) {
    headers['Idempotency-Key'] = o.key;
  }
  const tags = { journey: journey };
  if (o.name) {
    tags.name = o.name;
  }
  const params = { headers: headers, tags: tags };
  if (o.expect) {
    params.responseCallback = http.expectedStatuses.apply(null, o.expect);
  }
  const res = http.request(method, BASE + path, body ? JSON.stringify(body) : null, params);
  if (res.status === 429) {
    rateLimited.add(1, { journey: journey });
  }
  return res;
}

function pick(mix) {
  let n = Math.random() * 100;
  for (let i = 0; i < mix.length; i++) {
    n -= mix[i][1];
    if (n < 0) {
      return mix[i][0];
    }
  }
  return mix[mix.length - 1][0];
}

function productFor(hotShare) {
  if (Math.random() < hotShare) {
    return HOT;
  }
  return SPREAD[Math.floor(Math.random() * SPREAD.length)];
}

function shopperSub() {
  return sub(String(1 + Math.floor(Math.random() * POOL)));
}

function newKey(prefix) {
  return prefix + '-' + RUN + '-' + __VU + '-' + __ITER + '-' + Math.random().toString(36).slice(2, 10);
}

export function setup() {
  const who = sub('seed');
  const catalogue = [{ id: HOT, name: '[load-test] Doorbuster', sku: 'LT-HOT', price: 49.99, stock: HOT_STOCK }];
  SPREAD.forEach((id, i) => {
    catalogue.push({
      id: id,
      name: '[load-test] Product ' + (i + 1),
      sku: 'LT-' + id.slice(3).toUpperCase(),
      price: 10 + (i % 10),
      stock: SPREAD_STOCK,
    });
  });
  catalogue.forEach((p) => {
    const r = call('POST', '/api/inventory/products', who, 'setup', p, { expect: [201, 409] });
    if (r.status === 401) {
      fail('HTTP 401 from the gateway: JWT_SECRET is not the dev secret');
    }
    if (r.status !== 201 && r.status !== 409) {
      fail('Could not create ' + p.id + ': HTTP ' + r.status + ' ' + r.body);
    }
  });
  const hot = call('GET', '/api/inventory/products/' + HOT, who, 'setup', null, { name: 'GET /api/inventory/products/:id' });
  if (hot.status !== 200 || hot.json('available') < 50000) {
    fail('lt-hot is missing or nearly out of stock: ' + hot.status + ' ' + hot.body);
  }

  const orders = [];
  if (['l1', 'l2', 'hot', 'soak', 'spike', 'dashboard'].indexOf(TEST) !== -1) {
    for (let i = 1; i <= LOOKUP_ORDERS; i++) {
      const s = sub('lookup' + i);
      const r = call('POST', '/api/orders/', s, 'setup', { items: [{ product_id: SPREAD[i], quantity: 1 }] }, { key: RUN + '-setup-' + i });
      if (r.status !== 201) {
        fail('Setup order failed: HTTP ' + r.status + ' ' + r.body);
      }
      ordersCreated.add(1);
      orders.push({ id: r.json('id'), sub: s });
    }
  }
  return { orders: orders };
}

function browse() {
  const r = call('GET', '/api/inventory/products', shopperSub(), 'browse');
  check(r, { 'browse returns products': (x) => x.status === 200 && x.body.indexOf('"available"') !== -1 });
}

function detail(hotShare) {
  const r = call('GET', '/api/inventory/products/' + productFor(hotShare), shopperSub(), 'detail', null, { name: 'GET /api/inventory/products/:id' });
  check(r, { 'product found': (x) => x.status === 200 && x.body.indexOf('"available"') !== -1 });
}

function checkout(hotShare) {
  const body = { items: [{ product_id: productFor(hotShare), quantity: 1 }] };
  const r = call('POST', '/api/orders/', shopperSub(), 'checkout', body, { key: newKey('k') });
  const ok = check(r, { 'order created': (x) => x.status === 201 && x.body.indexOf('"id"') !== -1 });
  if (ok) {
    ordersCreated.add(1);
  }
}

function lookup(data) {
  const o = data.orders[Math.floor(Math.random() * data.orders.length)];
  const r = call('GET', '/api/orders/' + o.id, o.sub, 'lookup', null, { name: 'GET /api/orders/:id' });
  check(r, { 'order found': (x) => x.status === 200 && x.body.indexOf('"status"') !== -1 });
}

export function shopper(data) {
  const mix = MIXES[__ENV.MIX || 'normal'];
  const hotShare = Number(__ENV.HOT_SHARE || 0);
  const journey = pick(mix);
  if (journey === 'browse') {
    browse();
  } else if (journey === 'detail') {
    detail(hotShare);
  } else if (journey === 'checkout') {
    checkout(hotShare);
  } else {
    lookup(data);
  }
}

export function staff() {
  const headers = { Authorization: 'Bearer ' + tokenFor(ADMIN), 'Content-Type': 'application/json' };
  const reqs = STAFF_PATHS.map((p) => {
    const tags = { journey: 'staff' };
    if (p === '/dashboard/summary') {
      tags.name = 'staff-summary';
    }
    return ['GET', BASE + p, null, { headers: headers, tags: tags }];
  });
  const res = http.batch(reqs);
  res.forEach((r) => {
    if (r.status === 429) {
      rateLimited.add(1, { journey: 'staff' });
    }
  });
}

export function preflight() {
  const who = sub('preflight');
  ['orders', 'inventory', 'payments', 'notifications', 'shipping'].forEach((svc) => {
    const r = http.get(BASE + '/api/' + svc + '/healthz', { tags: { journey: 'preflight' } });
    const name = svc + ' is healthy';
    const sets = {};
    sets[name] = (x) => x.status === 200;
    check(r, sets);
  });

  const list = call('GET', '/api/inventory/products', who, 'preflight');
  check(list, { 'minted token is accepted and the catalogue answers': (x) => x.status === 200 && x.body.indexOf('"available"') !== -1 });

  const hot = call('GET', '/api/inventory/products/' + HOT, who, 'preflight');
  check(hot, { 'load-test products are seeded': (x) => x.status === 200 && x.json('available') >= 50000 });

  const key = newKey('pre');
  const body = { items: [{ product_id: HOT, quantity: 1 }] };
  const created = call('POST', '/api/orders/', who, 'preflight', body, { key: key });
  check(created, { 'order created (201) with id, status pending and total': (x) => x.status === 201 && x.json('status') === 'pending' && x.json('total') > 0 });
  const id = created.json('id');
  ordersCreated.add(1);

  const again = call('POST', '/api/orders/', who, 'preflight', body, { key: key });
  check(again, {
    'same Idempotency-Key returns the same order (200, replayed)': (x) => x.status === 200 && x.json('id') === id && x.headers['Idempotent-Replayed'] === 'true',
  });

  let status = 'pending';
  const t0 = Date.now();
  while (Date.now() - t0 < 90000) {
    status = call('GET', '/api/orders/' + id, who, 'preflight', null, { name: 'GET /api/orders/:id' }).json('status');
    if (status !== 'pending') {
      break;
    }
    sleep(1);
  }
  console.log('order ' + id + ' reached ' + status + ' after ' + ((Date.now() - t0) / 1000).toFixed(1) + ' s');
  check(1, { 'worker moved the order out of pending within 90 s': () => status === 'confirmed' || status === 'cancelled' });
}

export function ratelimit() {
  const a = sub('rla');
  const b = sub('rlb');

  let ok = 0;
  let limited = 0;
  let retryAfter = '';
  for (let i = 0; i < 260; i++) {
    const r = call('GET', '/api/inventory/products/' + SPREAD[0], a, 'ratelimit', null, { name: 'GET /api/inventory/products/:id', expect: [200, 429] });
    if (r.status === 200) {
      ok++;
    } else if (r.status === 429) {
      limited++;
      retryAfter = r.headers['Retry-After'] || retryAfter;
    }
  }
  let otherUserOk = 0;
  for (let i = 0; i < 5; i++) {
    const r = call('GET', '/api/inventory/products/' + SPREAD[0], b, 'ratelimit', null, { name: 'GET /api/inventory/products/:id', expect: [200, 429] });
    if (r.status === 200) {
      otherUserOk++;
    }
  }
  check(1, {
    'one user sending 260 requests in a few seconds is limited (429)': () => limited >= 20,
    'the 429 carries a Retry-After header': () => retryAfter !== '',
    'a second user is not affected': () => otherUserOk === 5,
    'at most 240 of the 260 got through': () => ok <= 240,
  });

  let unauthorized = 0;
  let badLimited = 0;
  for (let i = 0; i < 30; i++) {
    const r = http.get(BASE + '/api/inventory/products', {
      headers: { Authorization: 'Bearer not.a.token' },
      tags: { journey: 'ratelimit', name: 'GET /api/inventory/products (bad token)' },
      responseCallback: http.expectedStatuses(401, 429),
    });
    if (r.status === 401) {
      unauthorized++;
    } else if (r.status === 429) {
      badLimited++;
    }
  }
  check(1, {
    'bad tokens are refused (401) and then limited per client IP (429)': () => badLimited >= 1 && unauthorized + badLimited === 30,
  });

  let loginOk = 0;
  let loginLimited = 0;
  for (let i = 0; i < 25; i++) {
    const r = http.post(BASE + '/auth/login', JSON.stringify({ email: sub('login'), password: 'x' }), {
      headers: { 'Content-Type': 'application/json' },
      tags: { journey: 'ratelimit', name: 'POST /auth/login' },
      responseCallback: http.expectedStatuses(200, 429),
    });
    if (r.status === 200) {
      loginOk++;
    } else if (r.status === 429) {
      loginLimited++;
    }
  }
  check(1, {
    'login is limited per client IP (20 a minute)': () => loginLimited >= 1 && loginOk <= 20 && loginOk + loginLimited === 25,
  });
}

export function retry() {
  const who = sub('retry' + __ITER);
  const key = newKey('retry');
  const body = JSON.stringify({ items: [{ product_id: SPREAD[0], quantity: 1 }] });
  const headers = { Authorization: 'Bearer ' + tokenFor(who), 'Content-Type': 'application/json', 'Idempotency-Key': key };
  const reqs = [];
  for (let i = 0; i < 50; i++) {
    reqs.push(['POST', BASE + '/api/orders/', body, {
      headers: headers,
      tags: { journey: 'retry', name: 'POST /api/orders/ (same key)' },
      responseCallback: http.expectedStatuses(200, 201, 409),
    }]);
  }
  const res = http.batch(reqs);

  let created = 0;
  let other = 0;
  const ids = {};
  res.forEach((r) => {
    if (r.status === 201) {
      created++;
    } else if (r.status !== 200 && r.status !== 409) {
      other++;
    }
    if (r.status === 200 || r.status === 201) {
      ids[r.json('id')] = true;
    }
  });
  ordersCreated.add(created);
  const idList = Object.keys(ids);
  check(1, {
    'exactly one of 50 identical requests created the order': () => created === 1,
    'every other answer was a replay (200) or still-in-progress (409)': () => other === 0,
    'every replay points at that same order': () => idList.length === 1,
  });

  const mine = call('GET', '/api/orders/', who, 'retry', null, { name: 'GET /api/orders/' });
  check(mine, { 'the customer ends up with exactly one order': (x) => x.status === 200 && x.json().length === 1 });
}
