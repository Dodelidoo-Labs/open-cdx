// Calendar dates are interpreted in the viewer's selected timezone. Date
// objects used for chart buckets are UTC placeholders for those calendar dates.
(function (root) {
  const dayMs = 86400000;
  function preferredTimeZone(saved, browser, fallback = "UTC") {
    for (const name of [saved, browser, fallback, "UTC"]) {
      if (!name || name === "Local") continue;
      try {
        return new Intl.DateTimeFormat("en-US", { timeZone: name }).resolvedOptions().timeZone;
      } catch { /* Try the next available timezone. */ }
    }
  }
  function dayKey(value, timeZone = "UTC") {
    const parts = new Intl.DateTimeFormat("en-US", {
      timeZone, year: "numeric", month: "2-digit", day: "2-digit",
    }).formatToParts(new Date(value));
    const get = (type) => parts.find((part) => part.type === type).value;
    return `${get("year")}-${get("month")}-${get("day")}`;
  }
  function calendarDate(key) { return new Date(`${key}T00:00:00Z`); }
  function rolling(report, hours) {
    const to = new Date(report.generated_at);
    const from = new Date(to.getTime() - hours * 3600000);
    return {
      from, to, hours,
      start: calendarDate(dayKey(from, report.time_zone)),
      end: calendarDate(dayKey(to, report.time_zone)),
    };
  }
  function select(report, range) {
    if (!range.from) {
      const start = range.start.toISOString().slice(0, 10);
      const end = range.end.toISOString().slice(0, 10);
      const points = report.usage.filter((point) => point.date >= start && point.date <= end);
      return { points, complete: true };
    }
    const from = range.from.getTime(), to = range.to.getTime();
    // Legacy records represent an entire UTC day. Do not fabricate timestamps
    // or present a partial total as the complete rolling-window total.
    const legacy = report.untimed_usage || report.usage;
    const missing = legacy.some((point) => {
      const start = calendarDate(point.date).getTime();
      return start <= to && start + dayMs > from;
    });
    const points = report.rolling_usage?.[String(range.hours)] || [];
    return { points, complete: !missing };
  }
  function resets(report, range) {
    const start = range.start.toISOString().slice(0, 10), end = range.end.toISOString().slice(0, 10);
    return (report.allowance_resets || []).filter((reset) => {
      const at = new Date(reset.at);
      if (range.from) return at >= range.from && at <= range.to;
      const day = dayKey(at, report.time_zone);
      return day >= start && day <= end;
    });
  }
  const api = { preferredTimeZone, dayKey, rolling, select, resets };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.OpenCDXTelemetryRanges = api;
})(globalThis);
