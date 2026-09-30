const { test } = require('node:test');
const assert = require('node:assert/strict');
const { dayKey, resets, preferredTimeZone } = require('../static/telemetry-ranges.js');
const { filter } = require('../static/telemetry-devices.js');

const report = { time_zone: 'America/Argentina/Buenos_Aires', generated_at: '2026-09-09T00:55:00Z', usage: [] };

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

test('reset markers follow the exact visible window and machine scope', () => {
  const sample = { ...report, allowance_resets: [
    { at: '2026-09-08T00:54:59Z', source: 'history', device_id: 'a', usage: [] },
    { at: '2026-09-08T00:55:00Z', source: 'history', device_id: 'a', usage: [] },
    { at: '2026-09-09T00:54:00Z', source: 'history', device_id: 'b', usage: [] },
    { at: '2026-09-09T00:55:00Z', source: 'live', usage: [{ device_id: 'a', tokens: 1 }, { device_id: 'b', tokens: 9 }] },
    { at: '2026-09-09T00:55:01Z', source: 'history', device_id: 'a', usage: [] },
  ] };
  const window = { from: new Date('2026-09-08T00:55:00Z'), to: new Date('2026-09-09T00:55:00Z') };
  assert.equal(resets(sample, window).length, 3);
  const selected = filter(sample, 'a');
  const visible = resets(selected, window);
  assert.equal(visible.length, 2);
  assert.deepEqual(visible[1].usage, [{ device_id: 'a', tokens: 1 }]);
  const calendar = { start: new Date('2026-09-08T00:00:00Z'), end: new Date('2026-09-08T00:00:00Z') };
  assert.equal(resets(sample, calendar).length, 3);
  assert.equal(resets({ ...sample, allowance_resets: undefined }, calendar).length, 0);
});

test('viewer timezone prefers a saved choice, then the browser, then the server fallback', () => {
  assert.equal(preferredTimeZone('Europe/London', 'Asia/Tokyo', 'UTC'), 'Europe/London');
  assert.equal(preferredTimeZone('', 'Europe/London', 'UTC'), 'Europe/London');
  assert.equal(preferredTimeZone('invalid', 'invalid', 'Europe/London'), 'Europe/London');
  assert.equal(preferredTimeZone('Local', '', 'invalid'), 'UTC');
});
