// Calendar dates are interpreted in the viewer's selected timezone.
(function (root) {
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
  // A range is either an exact window ({from, to}) or calendar days ({start, end}).
  function resets(report, range) {
    const all = report.allowance_resets || [];
    if (range.from) return all.filter((reset) => new Date(reset.at) >= range.from && new Date(reset.at) <= range.to);
    const start = range.start.toISOString().slice(0, 10), end = range.end.toISOString().slice(0, 10);
    return all.filter((reset) => {
      const day = dayKey(reset.at, report.time_zone);
      return day >= start && day <= end;
    });
  }
  const api = { preferredTimeZone, dayKey, resets };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.OpenCDXTelemetryRanges = api;
})(globalThis);
