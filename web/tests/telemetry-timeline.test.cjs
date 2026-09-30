const { test } = require('node:test');
const assert = require('node:assert/strict');
const T = require('../static/telemetry-timeline.js');

const zone = 'America/Argentina/Buenos_Aires';
const hourly = (at, tokens, model = 'm') => ({ at, date: at.slice(0, 10), model, requests: 1, input_tokens: tokens, output_tokens: 0, cached_input_tokens: 1 });
const report = {
  time_zone: zone,
  generated_at: '2026-09-29T20:30:00Z',
  hourly_usage: [
    hourly('2026-09-29T19:00:00Z', 5),
    hourly('2026-09-29T17:00:00Z', 7, 'n'),
    hourly('2026-09-22T12:00:00Z', 11),
    hourly('2025-01-10T12:00:00Z', 13),
  ],
  untimed_usage: [{ date: '2024-06-01', model: 'm', requests: 1, input_tokens: 17, output_tokens: 0 }],
  allowance_history: [],
};

test('presets set how much is visible, anchored at the report time', () => {
  const now = Date.parse(report.generated_at);
  assert.deepEqual(T.presetView('rolling24', report), { from: now - T.day, to: now });
  assert.deepEqual(T.presetView('week', report), { from: now - 7 * T.day, to: now });
  const all = T.presetView('all', report);
  assert.equal(all.from, Date.parse('2024-06-01T00:00:00Z'));
  assert.equal(all.to, now);
});

test('panning moves through history without leaving recorded time', () => {
  const view = T.presetView('rolling24', report);
  const earlier = T.pan(view, -7 * T.day, report);
  assert.equal(earlier.to - earlier.from, T.day);
  assert.equal(earlier.to, view.to - 7 * T.day);
  const visible = T.visible(report, earlier).points;
  assert.deepEqual(visible.map((point) => point.input_tokens), [11]);
  assert.deepEqual(T.pan(view, T.day, report), view, 'cannot scroll past now');
  const oldest = T.pan(view, -100 * 365 * T.day, report);
  assert.equal(oldest.from, Date.parse('2024-06-01T00:00:00Z'));
});

test('zoom keeps the anchor in place and respects limits', () => {
  const view = T.presetView('week', report);
  const anchor = view.from + (view.to - view.from) / 2;
  const zoomed = T.zoom(view, 0.5, anchor, report);
  assert.equal(zoomed.to - zoomed.from, 3.5 * T.day);
  assert.equal((zoomed.from + zoomed.to) / 2, anchor);
  const tight = T.zoom(view, 0.00001, anchor, report);
  assert.equal(tight.to - tight.from, 2 * T.hour);
});

test('bars have one size per zoom level and follow local calendar boundaries', () => {
  assert.equal(T.unitFor(T.day), 'hour');
  assert.equal(T.unitFor(7 * T.day), 'day');
  assert.equal(T.unitFor(365 * T.day), 'week');
  assert.equal(T.unitFor(3 * 365 * T.day), 'month');
  const week = T.buckets(T.presetView('week', report), 'day', zone);
  assert.equal(week.length, 8);
  for (const bucket of week) {
    assert.equal(bucket.to - bucket.from, T.day);
    assert.equal(new Date(bucket.from).toISOString().slice(11, 16), '03:00', 'local midnight in Buenos Aires');
  }
  const monday = T.floor(Date.parse('2026-09-24T12:00:00Z'), 'week', zone);
  assert.equal(new Date(monday).toISOString(), '2026-09-21T03:00:00.000Z');
  const month = T.floor(Date.parse('2026-09-24T12:00:00Z'), 'month', zone);
  assert.equal(new Date(T.next(month, 'month', zone)).toISOString(), '2026-10-01T03:00:00.000Z');
});

test('day buckets stay aligned across daylight-saving changes', () => {
  const view = { from: Date.parse('2026-03-06T12:00:00Z'), to: Date.parse('2026-03-10T12:00:00Z') };
  const days = T.buckets(view, 'day', 'America/New_York');
  assert.deepEqual(days.map((bucket) => (bucket.to - bucket.from) / T.hour), [24, 24, 23, 24, 24]);
});

test('aggregation fills buckets and keeps daily-only history out of hours', () => {
  const values = (point) => point.input_tokens;
  const day = T.aggregate(report, T.presetView('rolling24', report), 'hour', zone, (point) => point.model, values);
  const totals = day.buckets.map((bucket) => [...bucket.series.values()].reduce((a, b) => a + b, 0));
  assert.equal(totals.reduce((a, b) => a + b, 0), 12);
  assert.equal(day.hiddenUntimed, false);
  const legacyDay = { from: Date.parse('2024-06-01T05:00:00Z'), to: Date.parse('2024-06-01T09:00:00Z') };
  assert.equal(T.aggregate(report, legacyDay, 'hour', zone, (p) => p.model, values).hiddenUntimed, true);
  const years = T.aggregate(report, T.presetView('all', report), 'month', zone, (p) => p.model, values);
  assert.equal(years.buckets.reduce((sum, bucket) => sum + (bucket.series.get('m') || 0), 0), 5 + 11 + 13 + 17);
  const partial = T.visible(report, legacyDay);
  assert.equal(partial.partial, true);
  assert.equal(partial.points.length, 1);
});
