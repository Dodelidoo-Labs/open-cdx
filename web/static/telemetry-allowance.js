// Account-wide observations stay independent of machine-filtered usage.
(function (root) {
  const maxGap = 15 * 60 * 1000;
  const palette = ["#2dab70", "#9065bf", "#cb7020", "#2289a4", "#d35682", "#777c2b"];
  const formatters = new Map(),
    midnights = new Map();
  function dayKey(value, timeZone) {
    if (!formatters.has(timeZone))
      formatters.set(timeZone, new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }));
    const parts = formatters.get(timeZone).formatToParts(new Date(value));
    const part = (name) => parts.find((p) => p.type === name).value;
    return `${part("year")}-${part("month")}-${part("day")}`;
  }
  // Find the actual start of the local calendar date, including DST days.
  function dayStart(key, timeZone = "UTC") {
    const cacheKey = `${timeZone}:${key}`;
    if (midnights.has(cacheKey)) return midnights.get(cacheKey);
    const center = Date.parse(`${key}T00:00:00Z`);
    let low = center - 36 * 3600000,
      high = center + 36 * 3600000;
    while (high - low > 1) {
      const mid = Math.floor((low + high) / 2);
      if (dayKey(mid, timeZone) < key) low = mid;
      else high = mid;
    }
    midnights.set(cacheKey, high);
    return high;
  }
  function bounds(range, timeZone) {
    const next = new Date(+range.end + 86400000).toISOString().slice(0, 10);
    return {
      from: range.from ? +range.from : dayStart(range.start.toISOString().slice(0, 10), timeZone),
      to: range.to ? +range.to : dayStart(next, timeZone),
    };
  }
  function windows(report) {
    const found = new Map();
    for (const series of report.allowance_history || []) found.set(series.window_seconds, series.window_label);
    return [...found].sort((a, b) => b[0] - a[0]).map(([seconds, label]) => ({ seconds, label }));
  }
  function select(report, range, seconds) {
    const { from, to } = bounds(range, report.time_zone || "UTC");
    const accounts = [...new Set((report.allowance_history || []).map((s) => s.account_id))].sort();
    return (report.allowance_history || [])
      .filter((s) => s.window_seconds === Number(seconds))
      .map((s) => ({
        ...s,
        color: palette[accounts.indexOf(s.account_id) % palette.length],
        points: s.points.filter(
          (p) =>
            Date.parse(p.at) >= from &&
            (Date.parse(p.at) < to || (range.to && Date.parse(p.at) === to)) &&
            Date.parse(p.at) <= Date.parse(report.generated_at),
        ),
      }));
  }
  function segments(points) {
    const result = [];
    for (const point of points) {
      const current = result[result.length - 1];
      const previous = current?.[current.length - 1];
      // Never bridge a collection outage, or continue a stale expired window.
      if (
        !previous ||
        Date.parse(point.at) - Date.parse(previous.at) > maxGap ||
        (point.reset_at === previous.reset_at && Date.parse(point.at) >= Date.parse(previous.reset_at))
      )
        result.push([point]);
      else current.push(point);
    }
    return result;
  }
  function nearest(points, at) {
    let low = 0,
      high = points.length;
    while (low < high) {
      const mid = (low + high) >>> 1;
      if (Date.parse(points[mid].at) < at) low = mid + 1;
      else high = mid;
    }
    const candidates = [points[low - 1], points[low]]
      .filter(Boolean)
      .sort((a, b) => Math.abs(Date.parse(a.at) - at) - Math.abs(Date.parse(b.at) - at));
    const point = candidates[0];
    return point && Math.abs(Date.parse(point.at) - at) <= maxGap / 2 ? point : null;
  }
  const api = { maxGap, dayStart, bounds, windows, select, segments, nearest };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.OpenCDXTelemetryAllowance = api;
})(globalThis);
