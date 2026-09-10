const { test } = require('node:test');
const assert = require('node:assert/strict');
const { dayKey, rolling, select } = require('../static/telemetry-ranges.js');
const { filter } = require('../static/telemetry-devices.js');

const point = (at, tokens, device = 'a') => ({
  recorded_at: at, date: dayKey(at, 'America/Argentina/Buenos_Aires'),
  input_tokens: tokens, output_tokens: 0, requests: 1, device_id: device,
});
const report = {
  time_zone: 'America/Argentina/Buenos_Aires',
  generated_at: '2026-09-09T00:55:00Z',
  usage: [], untimed_usage: [],
  rolling_usage: { '24': [
    point('2026-09-08T00:55:00Z', 1),
    point('2026-09-08T16:00:00Z', 365000000),
    point('2026-09-09T00:30:00Z', 35000000),
  ] },
};
test('24h uses server totals and configured calendar dates across UTC midnight', () => {
  const range = rolling(report, 24);
  assert.equal(range.to - range.from, 86400000);
  assert.equal(range.end.toISOString(), '2026-09-08T00:00:00.000Z');
  const result = select(report, range);
  assert.equal(result.complete, true);
  assert.equal(result.points.reduce((sum, p) => sum + p.input_tokens, 0), 400000001);
});
test('configured timezone controls calendar dates regardless of host timezone', () => {
  const original = process.env.TZ;
  try {
    process.env.TZ = 'Asia/Tokyo';
    assert.equal(dayKey(report.generated_at, report.time_zone), '2026-09-08');
    assert.equal(dayKey(report.generated_at, 'UTC'), '2026-09-09');
  } finally {
    if (original === undefined) delete process.env.TZ;
    else process.env.TZ = original;
  }
});
test('rolling durations remain exact through both daylight-saving transitions', () => {
  for (const generated_at of ['2026-03-08T20:00:00Z', '2026-11-01T20:00:00Z']) {
    for (const hours of [24, 168, 720]) {
      const range = rolling({ ...report, time_zone: 'America/New_York', generated_at }, hours);
      assert.equal(range.to - range.from, hours * 3600000);
    }
  }
});
test('legacy daily history makes overlapping rolling totals unavailable, not artificially low', () => {
  const legacy = { ...report, untimed_usage: [{ date: '2026-09-08', requests: 100 }] };
  assert.equal(select(legacy, rolling(legacy, 24)).complete, false);
  const old = { ...report, untimed_usage: [{ date: '2026-09-01' }] };
  assert.equal(select(old, rolling(old, 24)).complete, true);
});
test('machine filter scopes timed and legacy history before choosing a range', () => {
  const source = { ...report,
    rolling_usage: { '24': [...report.rolling_usage['24'], point('2026-09-09T00:30:00Z', 5000, 'b')] },
    untimed_usage: [{ date: '2026-09-08', device_id: 'b' }],
  };
  const selected = filter(source, 'a');
  assert.equal(select(selected, rolling(selected, 24)).complete, true);
  assert.equal(selected.rolling_usage['24'].length, report.rolling_usage['24'].length);
});


test('server-aggregated rolling totals retain machine isolation and export rows', () => {
  const source = { ...report, rolling_usage: { '24': [
    { ...point('2026-09-09T00:30:00Z', 400000000), recorded_at: undefined },
    { ...point('2026-09-09T00:30:00Z', 50000000, 'b'), recorded_at: undefined },
  ] } };
  const selected = filter(source, 'a');
  assert.deepEqual(select(selected, rolling(selected, 24)).points, [source.rolling_usage['24'][0]]);
});

test('reset markers obey server timezone, exact rolling bounds and machine scope', () => {
  const { resets } = require('../static/telemetry-ranges.js');
  const sample = { ...report, allowance_resets: [
    { at: '2026-09-08T00:54:59Z', source: 'history', device_id: 'a', usage: [] },
    { at: '2026-09-08T00:55:00Z', source: 'history', device_id: 'a', usage: [] },
    { at: '2026-09-09T00:54:00Z', source: 'history', device_id: 'b', usage: [] },
    { at: '2026-09-09T00:55:00Z', source: 'live', usage: [{ device_id: 'a', tokens: 1 }, { device_id: 'b', tokens: 9 }] },
    { at: '2026-09-09T00:55:01Z', source: 'history', device_id: 'a', usage: [] },
  ] };
  assert.equal(resets(sample, rolling(sample, 24)).length, 3);
  const selected = filter(sample, 'a');
  const visible = resets(selected, rolling(sample, 24));
  assert.equal(visible.length, 2);
  assert.deepEqual(visible[1].usage, [{ device_id: 'a', tokens: 1 }]);
  const calendar = { start: new Date('2026-09-08T00:00:00Z'), end: new Date('2026-09-08T00:00:00Z') };
  assert.equal(resets(sample, calendar).length, 3);
  assert.equal(resets({ ...sample, allowance_resets: undefined }, calendar).length, 0);
});

test('viewer timezone prefers a saved choice, then the browser, then the server fallback', () => {
  const { preferredTimeZone } = require('../static/telemetry-ranges.js');
  assert.equal(preferredTimeZone('Europe/London', 'Asia/Tokyo', 'UTC'), 'Europe/London');
  assert.equal(preferredTimeZone('', 'Europe/London', 'UTC'), 'Europe/London');
  assert.equal(preferredTimeZone('invalid', 'invalid', 'Europe/London'), 'Europe/London');
  assert.equal(preferredTimeZone('Local', '', 'invalid'), 'UTC');
});

test('London and Buenos Aires share rolling instants but use different calendar dates', () => {
  const london = rolling({ ...report, time_zone: 'Europe/London' }, 24);
  const buenosAires = rolling(report, 24);
  assert.equal(+london.from, +buenosAires.from);
  assert.equal(+london.to, +buenosAires.to);
  assert.equal(london.end.toISOString(), '2026-09-09T00:00:00.000Z');
  assert.equal(buenosAires.end.toISOString(), '2026-09-08T00:00:00.000Z');
});
