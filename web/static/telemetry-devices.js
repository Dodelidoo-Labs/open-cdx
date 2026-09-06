// Shared by the dashboard and its isolated Node tests.
(function (root) {
  function devices(report) {
    const names = new Map();
    for (const point of report.usage) {
      const id = point.device_id || "";
      names.set(id, id ? `${point.device_name || "Removed device"} · ${id}` : "Unknown device");
    }
    return Array.from(names, ([id, name]) => ({ id, name }))
      .sort((a, b) => a.name.localeCompare(b.name));
  }

  function filter(report, deviceID) {
    if (deviceID === "*") return report;
    const usage = report.usage.filter((point) => (point.device_id || "") === deviceID);
    const counts = new Map();
    let requests = 0, input = 0, output = 0;
    for (const point of usage) {
      counts.set(point.date, (counts.get(point.date) || 0) + point.requests);
      requests += point.requests;
      input += point.input_tokens;
      output += point.output_tokens;
    }
    return {
      ...report, usage,
      activity: Array.from(counts, ([date, requests]) => ({ date, requests })).sort((a, b) => a.date.localeCompare(b.date)),
      total_requests: requests, total_input_tokens: input, total_output_tokens: output,
      reconciliation: report.reconciliation?.device_id === deviceID ? report.reconciliation : null,
    };
  }

  const api = { devices, filter };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.OpenCDXTelemetryDevices = api;
})(globalThis);
