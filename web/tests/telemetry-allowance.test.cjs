const test = require("node:test");
const assert = require("node:assert/strict");
const allowance = require("../static/telemetry-allowance.js");
const { filter } = require("../static/telemetry-devices.js");
const point = (at, remaining = 60, reset = "2026-09-25T00:00:00Z") => ({ at, remaining, reset_at: reset });
const series = {
  account_id: "a",
  label: "Masked account",
  window_seconds: 604800,
  window_label: "Weekly",
  points: [point("2026-09-18T12:00:00Z"), point("2026-09-18T12:05:00Z"), point("2026-09-18T13:00:00Z")],
};
const report = {
  generated_at: "2026-09-19T00:00:00Z",
  time_zone: "UTC",
  usage: [],
  allowance_history: [series, { ...series, window_seconds: 18000, window_label: "5 hours" }],
};
test("selects real account readings by time and window without machine attribution", () => {
  const range = {
    from: new Date("2026-09-18T12:05:00Z"),
    to: new Date("2026-09-18T12:30:00Z"),
    start: new Date("2026-09-18"),
    end: new Date("2026-09-18"),
  };
  const got = allowance.select(filter(report, "different-machine"), range, 604800);
  assert.deepEqual(got[0].points, [series.points[1]]);
  assert.equal(got.length, 1);
  assert.deepEqual(
    allowance.windows(report).map((w) => w.label),
    ["Weekly", "5 hours"],
  );
  const other = { ...report, allowance_history: [{ ...series, account_id: "b" }, series] };
  assert.equal(allowance.select(other, range, 604800).find((s) => s.account_id === "a").color, got[0].color);
});
test("keeps outages as gaps, preserves reset observations and does not invent hover balances", () => {
  const points = [
    point("2026-09-18T12:00:00Z", 5, "2026-09-18T12:03:00Z"),
    point("2026-09-18T12:05:00Z", 95),
    point("2026-09-18T13:00:00Z", 80),
  ];
  assert.deepEqual(
    allowance.segments(points).map((s) => s.length),
    [2, 1],
  );
  assert.equal(allowance.nearest(points, Date.parse("2026-09-18T12:40:00Z")), null);
  assert.equal(allowance.nearest(points, Date.parse("2026-09-18T12:04:00Z")), points[1]);
  assert.deepEqual(
    allowance.segments([points[0], { ...points[1], reset_at: points[0].reset_at }]).map((s) => s.length),
    [1, 1],
  );
});
test("calendar geometry follows timezone and actual DST day lengths", () => {
  const range = (day) => ({ start: new Date(day), end: new Date(day) });
  const spring = allowance.bounds(range("2026-03-08"), "America/New_York");
  const fall = allowance.bounds(range("2026-11-01"), "America/New_York");
  assert.equal(spring.to - spring.from, 23 * 3600000);
  assert.equal(fall.to - fall.from, 25 * 3600000);
  assert.equal(new Date(allowance.dayStart("2026-09-19", "America/Argentina/Buenos_Aires")).toISOString(), "2026-09-19T03:00:00.000Z");
  const local = {
    ...report,
    time_zone: "America/Argentina/Buenos_Aires",
    allowance_history: [{ ...series, points: [point("2026-09-18T01:00:00Z"), point("2026-09-18T04:00:00Z")] }],
  };
  assert.deepEqual(allowance.select(local, range("2026-09-18"), 604800)[0].points, [local.allowance_history[0].points[1]]);
});
