// Shared by the dashboard and its isolated Node tests.
(function (root) {
  function devices(report) {
    const names = new Map();
    for (const point of report.usage) {
      const id = point.device_id || "";
      names.set(id, id ? `${point.device_name || "Removed device"} · ${id}` : "Unknown device");
    }
    for (const reset of report.allowance_resets || []) {
      if (reset.device_id && !names.has(reset.device_id)) names.set(reset.device_id, `${reset.label} · ${reset.device_id}`);
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
      allowance_resets: (report.allowance_resets || []).filter((reset) => reset.source === "live" || reset.device_id === deviceID)
        .map((reset) => ({ ...reset, usage: reset.usage.filter((row) => row.device_id === deviceID) })),
      rolling_usage: report.rolling_usage && Object.fromEntries(Object.entries(report.rolling_usage).map(([hours, points]) => [hours, points.filter((point) => (point.device_id || "") === deviceID)])),
      untimed_usage: report.untimed_usage?.filter((point) => (point.device_id || "") === deviceID),
      activity: Array.from(counts, ([date, requests]) => ({ date, requests })).sort((a, b) => a.date.localeCompare(b.date)),
      total_requests: requests, total_input_tokens: input, total_output_tokens: output,
      reconciliation: report.reconciliation?.device_id === deviceID ? report.reconciliation : null,
    };
  }

  const api = { devices, filter };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.OpenCDXTelemetryDevices = api;
})(globalThis);
