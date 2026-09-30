// A scrollable, zoomable usage timeline. Presets choose how much time is
// visible; the view can then be moved through all recorded history. Buckets
// follow calendar boundaries in the viewer's timezone, so bars of one size
// are evenly spaced.
(function (root) {
  const node = typeof module !== "undefined" && module.exports;
  const Ranges = node ? require("./telemetry-ranges.js") : root.OpenCDXTelemetryRanges;
  const Allowance = node ? require("./telemetry-allowance.js") : root.OpenCDXTelemetryAllowance;
  const hour = 3600000;
  const day = 24 * hour;
  const minimumSpan = 2 * hour;
  const presets = { rolling24: day, week: 7 * day, thirty: 30 * day, year: 365 * day };

  function unitFor(span) {
    if (span <= 2 * day + hour) return "hour";
    if (span <= 62 * day) return "day";
    if (span <= 550 * day) return "week";
    return "month";
  }

  function shiftKey(key, days, months = 0) {
    const date = new Date(`${key}T00:00:00Z`);
    if (months) date.setUTCMonth(date.getUTCMonth() + months, 1);
    date.setUTCDate(date.getUTCDate() + days);
    return date.toISOString().slice(0, 10);
  }

  function floor(at, unit, timeZone) {
    if (unit === "hour") return Math.floor(at / hour) * hour;
    const key = Ranges.dayKey(at, timeZone);
    if (unit === "day") return Allowance.dayStart(key, timeZone);
    if (unit === "week") {
      const weekday = (new Date(`${key}T00:00:00Z`).getUTCDay() + 6) % 7;
      return Allowance.dayStart(shiftKey(key, -weekday), timeZone);
    }
    return Allowance.dayStart(`${key.slice(0, 8)}01`, timeZone);
  }

  function next(start, unit, timeZone) {
    if (unit === "hour") return start + hour;
    const key = Ranges.dayKey(start, timeZone);
    if (unit === "day") return Allowance.dayStart(shiftKey(key, 1), timeZone);
    if (unit === "week") return Allowance.dayStart(shiftKey(key, 7), timeZone);
    return Allowance.dayStart(shiftKey(`${key.slice(0, 8)}01`, 0, 1), timeZone);
  }

  // Every bucket that intersects the view, including partial edge buckets.
  function buckets(view, unit, timeZone) {
    const result = [];
    for (let start = floor(view.from, unit, timeZone); start < view.to; ) {
      const end = next(start, unit, timeZone);
      result.push({ from: start, to: end });
      start = end;
    }
    return result;
  }

  // Legacy rows carry only a UTC day. They count wherever that day overlaps.
  function untimedSpan(point) {
    const from = Date.parse(`${point.date}T00:00:00Z`);
    return { from, to: from + day };
  }

  function extent(report) {
    const now = Date.parse(report.generated_at);
    let earliest = now;
    for (const point of report.hourly_usage || []) earliest = Math.min(earliest, Date.parse(point.at));
    for (const point of report.untimed_usage || []) earliest = Math.min(earliest, untimedSpan(point).from);
    for (const series of report.allowance_history || [])
      for (const point of series.points) earliest = Math.min(earliest, Date.parse(point.at));
    // Presets stay available with little history; older time is simply empty.
    return { from: Math.min(earliest, now - presets.year), to: now, earliest };
  }

  function clamp(view, bounds) {
    const span = Math.min(Math.max(view.to - view.from, minimumSpan), bounds.to - bounds.from);
    let to = Math.min(view.to, bounds.to);
    let from = to - span;
    if (from < bounds.from) {
      from = bounds.from;
      to = from + span;
    }
    return { from, to };
  }

  function presetView(preset, report) {
    const bounds = extent(report);
    const now = bounds.to;
    const today = Ranges.dayKey(now, report.time_zone);
    if (presets[preset]) return clamp({ from: now - presets[preset], to: now }, bounds);
    if (preset === "today") return clamp({ from: Allowance.dayStart(today, report.time_zone), to: now }, bounds);
    if (preset === "month") return clamp({ from: Allowance.dayStart(`${today.slice(0, 8)}01`, report.time_zone), to: now }, bounds);
    return clamp({ from: Math.min(bounds.earliest, now - day), to: now }, bounds);
  }

  function pan(view, delta, report) {
    return clamp({ from: view.from + delta, to: view.to + delta }, extent(report));
  }

  function zoom(view, factor, anchor, report) {
    const bounds = extent(report);
    const span = Math.min(Math.max((view.to - view.from) * factor, minimumSpan), bounds.to - bounds.from);
    const ratio = (anchor - view.from) / (view.to - view.from);
    return clamp({ from: anchor - ratio * span, to: anchor - ratio * span + span }, bounds);
  }

  // Rows inside the view for totals, breakdown and export. `partial` marks
  // legacy daily rows that extend past the view.
  function visible(report, view) {
    const points = [];
    let partial = false;
    for (const point of report.hourly_usage || []) {
      const at = Date.parse(point.at);
      if (at >= view.from && at < view.to) points.push(point);
    }
    for (const point of report.untimed_usage || []) {
      const span = untimedSpan(point);
      if (span.to <= view.from || span.from >= view.to) continue;
      points.push(point);
      partial ||= span.from < view.from || span.to > view.to;
    }
    return { points, partial };
  }

  // Sum rows into the view's buckets. Legacy daily rows cannot be placed in
  // an hour; they are reported through `hiddenUntimed` instead.
  function aggregate(report, view, unit, timeZone, keyOf, valueOf) {
    const list = buckets(view, unit, timeZone).map((bucket) => ({ ...bucket, series: new Map(), cached: new Map() }));
    const find = (at) => {
      let low = 0, high = list.length - 1;
      while (low <= high) {
        const middle = (low + high) >> 1;
        if (at < list[middle].from) high = middle - 1;
        else if (at >= list[middle].to) low = middle + 1;
        else return list[middle];
      }
      return null;
    };
    const add = (bucket, point) => {
      const key = keyOf(point);
      bucket.series.set(key, (bucket.series.get(key) || 0) + valueOf(point));
      bucket.cached.set(key, (bucket.cached.get(key) || 0) + (point.cached_input_tokens || 0));
    };
    for (const point of report.hourly_usage || []) {
      const bucket = find(Date.parse(point.at));
      if (bucket) add(bucket, point);
    }
    let hiddenUntimed = false;
    for (const point of report.untimed_usage || []) {
      const span = untimedSpan(point);
      if (span.to <= view.from || span.from >= view.to) continue;
      if (unit === "hour") {
        hiddenUntimed = true;
        continue;
      }
      const bucket = find(Math.min(Math.max(span.from + day / 2, view.from), view.to - 1));
      if (bucket) add(bucket, point);
    }
    return { buckets: list, hiddenUntimed };
  }

  const api = { hour, day, presets, unitFor, floor, next, buckets, extent, clamp, presetView, pan, zoom, visible, aggregate };
  if (node) module.exports = api;
  else root.OpenCDXTelemetryTimeline = api;
})(globalThis);
