const { test } = require('node:test');
const assert = require('node:assert/strict');
const { devices, filter } = require('../static/telemetry-devices.js');
const point = (id, count, date = '2026-09-01') => ({ device_id: id, device_name: 'Mac', date, provider: 'openai', model: 'same', requests: count, input_tokens: count * 10, output_tokens: count * 2 });
const report = {
  generated_at: '2026-09-02T00:00:00Z',
  usage: [point('a', 1), point('a', 2), point('b', 4), point('', 8)],
  activity: [{ date: '2026-09-01', requests: 15 }],
  reconciliation: { device_id: 'b' },
};
test('machine filter scopes chart/export rows, metrics, and activity consistently', () => {
  const selected = filter(report, 'a');
  assert.equal(selected.usage.length, 2);
  assert.equal(selected.total_requests, 3);
  assert.equal(selected.total_input_tokens, 30);
  assert.equal(selected.total_output_tokens, 6);
  assert.deepEqual(selected.activity, [{ date: '2026-09-01', requests: 3 }]);
  assert.equal(selected.reconciliation, null);
  assert.equal(filter(report, 'b').reconciliation, report.reconciliation);
  assert.equal(filter(report, '*'), report);
  assert.equal(report.usage.length, 4);
});
test('unknown and absent machines do not leak another machine’s usage', () => {
  assert.equal(filter(report, '').total_requests, 8);
  assert.deepEqual(filter(report, 'absent').activity, []);
  assert.equal(filter(report, 'absent').total_requests, 0);
});
test('duplicate names stay distinguishable and removed devices retain identity', () => {
  const options = devices({ usage: [...report.usage, { ...point('removed', 1), device_name: '' }] });
  assert.equal(options.length, 4);
  assert.equal(options.find(d => d.id === 'a').name, 'Mac · a');
  assert.equal(options.find(d => d.id === 'b').name, 'Mac · b');
  assert.equal(options.find(d => d.id === '').name, 'Unknown device');
  assert.equal(options.find(d => d.id === 'removed').name, 'Removed device · removed');
});
