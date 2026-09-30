(() => {
  const root = document.documentElement;
  const storedTheme = (() => {
    try {
      return window.localStorage.getItem("ocdx-theme");
    } catch (_) {
      return "";
    }
  })();
  const requestedTheme = new URLSearchParams(window.location.search).get("theme");
  const preferredTheme = window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
  root.dataset.theme = ["light", "dark"].includes(requestedTheme)
    ? requestedTheme
    : ["light", "dark"].includes(storedTheme) ? storedTheme : preferredTheme;

  const fallbackTimeZone = root.dataset.timeZone || "UTC";
  let savedTimeZone = "";
  try { savedTimeZone = localStorage.getItem("opencdx-timezone") || ""; } catch {}
  const browserTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  let selectedTimeZone = OpenCDXTelemetryRanges.preferredTimeZone(savedTimeZone, browserTimeZone, fallbackTimeZone);
  let localDateTime, localDate, localClock, localDateTimeTitle;
  function configureTimeFormats() {
    localDateTime = new Intl.DateTimeFormat(undefined, {
      timeZone: selectedTimeZone, month: "short", day: "numeric", hour: "numeric", minute: "2-digit",
    });
    localDate = new Intl.DateTimeFormat(undefined, { timeZone: selectedTimeZone, month: "short", day: "numeric" });
    localClock = new Intl.DateTimeFormat(undefined, { timeZone: selectedTimeZone, hour: "numeric", minute: "2-digit" });
    localDateTimeTitle = new Intl.DateTimeFormat(undefined, {
      timeZone: selectedTimeZone, dateStyle: "full", timeStyle: "long",
    });
  }
  configureTimeFormats();
  const localizeTime = (element, formatter) => {
    const value = new Date(element.dateTime);
    if (Number.isNaN(value.getTime())) return;
    element.textContent = formatter.format(value);
    element.title = localDateTimeTitle.format(value);
  };
  const localizeTimes = (scope = document) => {
    scope.querySelectorAll("time[data-local-datetime]").forEach((element) => localizeTime(element, localDateTime));
    scope.querySelectorAll("time[data-local-date]").forEach((element) => localizeTime(element, localDate));
    scope.querySelectorAll("time[data-local-clock]").forEach((element) => localizeTime(element, localClock));
  };
  localizeTimes();

  const tabNames = ["home", "logs", "instructions", "accounts", "providers", "devices", "catalog"];
  const tabTitles = {
    home: "Telemetry",
    logs: "Request logs",
    instructions: "Instruction history",
    accounts: "OpenAI accounts",
    providers: "Providers",
    devices: "Devices",
    catalog: "Catalog",
  };
  const tabs = Array.from(document.querySelectorAll("[data-tab]"));
  const panels = Array.from(document.querySelectorAll("[data-tab-panel]"));
  let selectedTab = "home";
  let refreshCoordinator = () => {};
  if (tabs.length && "scrollRestoration" in window.history) window.history.scrollRestoration = "manual";

  function rememberedTab() {
    const hashTab = window.location.hash.slice(1);
    if (tabNames.includes(hashTab)) return hashTab;
    try {
      const stored = window.sessionStorage.getItem("opencdx.admin.tab");
      if (tabNames.includes(stored)) return stored;
    } catch (_) {
      // Storage can be unavailable in hardened browser profiles.
    }
    return "home";
  }

  function selectTab(name, focus = false) {
    if (!tabNames.includes(name)) name = "home";
    const changed = selectedTab !== name;
    selectedTab = name;
    tabs.forEach((tab) => {
      const selected = tab.dataset.tab === name;
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
      if (selected && focus) tab.focus();
    });
    panels.forEach((panel) => {
      panel.hidden = panel.dataset.tabPanel !== name;
    });
    document.body.dataset.page = name;
    document.title = `${tabTitles[name]} · OpenCDX Router`;
    document.body.classList.remove("nav-open");
    document.querySelectorAll('input[name="return_tab"]').forEach((input) => {
      input.value = name;
    });
    try {
      window.sessionStorage.setItem("opencdx.admin.tab", name);
      window.history.replaceState(null, "", `#${name}`);
    } catch (_) {
      // Tabs still function when storage or history mutation is restricted.
    }
    window.requestAnimationFrame(() => window.scrollTo(0, 0));
    refreshCoordinator(changed);
  }

  tabs.forEach((tab, index) => {
    tab.addEventListener("click", () => selectTab(tab.dataset.tab));
    tab.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) return;
      event.preventDefault();
      const offset = event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : -1;
      const next = (index + offset + tabs.length) % tabs.length;
      selectTab(tabs[next].dataset.tab, true);
    });
  });
  if (tabs.length) {
    selectTab(rememberedTab());
    window.addEventListener("load", () => window.scrollTo(0, 0), { once: true });
  }

  document.querySelector("[data-tab-shortcut]")?.addEventListener("click", (event) => {
    selectTab(event.currentTarget.dataset.tabShortcut);
  });
  const themeToggle = document.querySelector("[data-theme-toggle]");
  const syncThemeToggle = () => {
    if (!themeToggle) return;
    const label = root.dataset.theme === "light" ? "Use dark theme" : "Use light theme";
    themeToggle.setAttribute("aria-label", label);
    themeToggle.dataset.tooltip = label;
  };
  syncThemeToggle();
  themeToggle?.addEventListener("click", () => {
    root.dataset.theme = root.dataset.theme === "light" ? "dark" : "light";
    try {
      window.localStorage.setItem("ocdx-theme", root.dataset.theme);
    } catch (_) {
      // Theme still changes when storage is unavailable.
    }
    syncThemeToggle();
  });
  document.querySelector("[data-menu-toggle]")?.addEventListener("click", () => {
    document.body.classList.toggle("nav-open");
  });
  document.querySelector("[data-mobile-backdrop]")?.addEventListener("click", () => {
    document.body.classList.remove("nav-open");
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") document.body.classList.remove("nav-open");
  });

  document.addEventListener("submit", (event) => {
    const form = event.target.closest("form[data-confirm]");
    if (form && !window.confirm(form.dataset.confirm)) event.preventDefault();
  });

  document.addEventListener("click", (event) => {
    const reveal = event.target.closest("[data-show-models]");
    if (reveal) {
      const list = reveal.closest("[data-model-list]");
      list.querySelectorAll("[data-extra-model]").forEach((pill) => {
        pill.hidden = false;
      });
      reveal.remove();
    }
    document.querySelectorAll("details.action-menu[open]").forEach((menu) => {
      if (!menu.contains(event.target)) menu.removeAttribute("open");
    });
  });

  document.querySelectorAll("[data-sort-table]").forEach((table) => {
    const buttons = Array.from(table.querySelectorAll("[data-sort]"));
    const tbody = table.tBodies[0];
    buttons.forEach((button) => {
      button.addEventListener("click", () => {
        const key = button.dataset.sort;
        const current = button.dataset.direction || "none";
        const direction = current === "ascending" ? "descending" : "ascending";
        const rows = Array.from(tbody.rows);
        rows.sort((left, right) => {
          const a = (left.dataset[key] || "").toLocaleLowerCase();
          const b = (right.dataset[key] || "").toLocaleLowerCase();
          const compared = a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" });
          return direction === "ascending" ? compared : -compared;
        });
        rows.forEach((row) => tbody.appendChild(row));
        buttons.forEach((other) => {
          other.dataset.direction = "none";
          other.closest("th").setAttribute("aria-sort", "none");
        });
        button.dataset.direction = direction;
        button.closest("th").setAttribute("aria-sort", direction);
      });
    });
  });

  document.querySelectorAll("form[data-refresh]").forEach((form) => {
    form.addEventListener("submit", () => {
      const button = form.querySelector("button");
      button.disabled = true;
      button.classList.add("is-loading");
      button.setAttribute("aria-label", `Refreshing ${button.textContent.trim().replace(/^Refresh\s+/i, "").toLowerCase()}`);
    });
  });

  document.querySelectorAll("[data-provider-toggle]").forEach((button) => {
    button.addEventListener("click", () => {
      const panel = document.querySelector(`[data-provider-config="${button.dataset.providerToggle}"]`);
      if (!panel) return;
      const opening = panel.hidden;
      panel.hidden = !opening;
      button.setAttribute("aria-expanded", String(opening));
      const label = opening ? "Close provider configuration" : "Configure provider";
      button.setAttribute("aria-label", label);
      button.dataset.tooltip = label;
      if (opening) panel.querySelector("input:not([type=hidden])")?.focus();
    });
  });

  const catalogFilter = document.querySelector("[data-catalog-filter]");
  const catalogState = document.querySelector("[data-state-filter]");
  const applyCatalogFilters = () => {
    const term = catalogFilter?.value.trim().toLocaleLowerCase() || "";
    const state = catalogState?.value || "all";
    let visible = 0;
    document.querySelectorAll("[data-model-row]").forEach((row) => {
      const matchesTerm = !term || row.textContent.toLocaleLowerCase().includes(term);
      const matchesState = state === "all" || row.dataset.state === state;
      row.hidden = !(matchesTerm && matchesState);
      if (!row.hidden) visible += 1;
    });
    const empty = document.querySelector("[data-catalog-empty]");
    if (empty) empty.hidden = visible !== 0;
  };
  catalogFilter?.addEventListener("input", applyCatalogFilters);
  catalogState?.addEventListener("change", applyCatalogFilters);

  const flash = document.querySelector("[data-flash]");
  if (flash) {
    const cleanURL = new URL(window.location.href);
    cleanURL.searchParams.delete("message");
    cleanURL.searchParams.delete("error");
    try {
      window.history.replaceState(window.history.state, "", `${cleanURL.pathname}${cleanURL.search}${cleanURL.hash}`);
    } catch (_) {
      // The notification can still be dismissed if history mutation is restricted.
    }
    let flashTimer;
    const dismissFlash = () => {
      window.clearTimeout(flashTimer);
      flash.remove();
    };
    flash.querySelector("[data-flash-dismiss]")?.addEventListener("click", dismissFlash);
    flashTimer = window.setTimeout(dismissFlash, flash.classList.contains("error") ? 10000 : 6000);
  }

  const accountList = document.querySelector("[data-account-list]");
  const accountOrderForm = document.querySelector("[data-account-order-form]");
  if (accountList && accountOrderForm) {
    let draggedRow = null;
    let originalRows = [];

    const accountRows = () => Array.from(accountList.querySelectorAll(":scope > [data-account-id]"));
    const orderChanged = () => originalRows.some((row, index) => accountRows()[index] !== row);
    const syncPositions = () => {
      const rows = accountRows();
      rows.forEach((row, index) => {
        row.setAttribute("aria-posinset", String(index + 1));
        row.setAttribute("aria-setsize", String(rows.length));
      });
    };
    const restoreOrder = () => {
      originalRows.forEach((row) => accountList.appendChild(row));
      syncPositions();
    };
    const moveRowAtY = (row, clientY) => {
      const sourceIndex = originalRows.indexOf(row);
      const candidates = accountRows().filter((candidate) => candidate !== row);
      const before = candidates.find((candidate) => {
        const bounds = candidate.getBoundingClientRect();
        const movingDown = sourceIndex < originalRows.indexOf(candidate);
        const threshold = movingDown ? 0.25 : 0.75;
        return clientY < bounds.top + bounds.height * threshold;
      });
      if (before) accountList.insertBefore(row, before);
      else accountList.appendChild(row);
      syncPositions();
    };
    const submitOrder = () => {
      const fields = accountOrderForm.querySelector("[data-account-order-fields]");
      fields.replaceChildren(...accountRows().map((row) => {
        const input = document.createElement("input");
        input.type = "hidden";
        input.name = "account_id";
        input.value = row.dataset.accountId;
        return input;
      }));
      accountList.classList.add("is-saving");
      if (typeof accountOrderForm.requestSubmit === "function") accountOrderForm.requestSubmit();
      else accountOrderForm.submit();
    };
    const beginDrag = (row) => {
      draggedRow = row;
      originalRows = accountRows();
      row.classList.add("is-dragging");
      accountList.classList.add("is-reordering");
    };
    const finishDrag = (commit) => {
      if (!draggedRow) return;
      const changed = orderChanged();
      draggedRow.classList.remove("is-dragging");
      accountList.classList.remove("is-reordering");
      if (!commit) restoreOrder();
      draggedRow = null;
      if (commit && changed) submitOrder();
    };

    accountRows().forEach((row) => {
      const handle = row.querySelector("[data-account-drag]");
      if (!handle) return;
      row.draggable = false;
      handle.draggable = true;

      handle.addEventListener("pointerdown", (event) => {
        if (event.pointerType === "mouse") return;
        event.preventDefault();
        beginDrag(row);
        handle.setPointerCapture?.(event.pointerId);
      });
      handle.addEventListener("pointermove", (event) => {
        if (draggedRow !== row || event.pointerType === "mouse") return;
        event.preventDefault();
        moveRowAtY(row, event.clientY);
      });
      handle.addEventListener("pointerup", (event) => {
        if (event.pointerType !== "mouse" && draggedRow === row) finishDrag(true);
      });
      handle.addEventListener("pointercancel", (event) => {
        if (event.pointerType !== "mouse" && draggedRow === row) finishDrag(false);
      });
      handle.addEventListener("keydown", (event) => {
        if (!["ArrowUp", "ArrowDown"].includes(event.key)) return;
        const movable = accountRows();
        const current = movable.indexOf(row);
        const next = current + (event.key === "ArrowDown" ? 1 : -1);
        if (next < 0 || next >= movable.length) return;
        event.preventDefault();
        originalRows = accountRows();
        const target = movable[next];
        if (next < current) accountList.insertBefore(row, target);
        else accountList.insertBefore(row, target.nextSibling);
        syncPositions();
        submitOrder();
      });

      handle.addEventListener("dragstart", (event) => {
        if (!draggedRow) beginDrag(row);
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/plain", row.dataset.accountId);
        event.dataTransfer.setDragImage?.(handle, handle.offsetWidth / 2, handle.offsetHeight / 2);
      });
      handle.addEventListener("dragend", () => finishDrag(false));
    });

    accountList.addEventListener("dragover", (event) => {
      if (!draggedRow) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      moveRowAtY(draggedRow, event.clientY);
    });
    accountList.addEventListener("drop", (event) => {
      if (!draggedRow) return;
      event.preventDefault();
      finishDrag(true);
    });
    syncPositions();
  }

  const telemetryRoot = document.querySelector("[data-telemetry]");
  if (!telemetryRoot) return;

  const fullDate = new Intl.DateTimeFormat(undefined, { year: "numeric", month: "long", day: "numeric", timeZone: "UTC" });
  const svgNS = "http://www.w3.org/2000/svg";
  const tooltip = document.createElement("div");
  tooltip.className = "telemetry-tooltip";
  tooltip.setAttribute("role", "tooltip");
  document.body.appendChild(tooltip);

  function formatNumber(value, maximumFractionDigits = 0) {
    const numeric = Number(value);
    if (!Number.isFinite(numeric)) return "0";
    const absolute = Math.abs(numeric);
    const fixed = maximumFractionDigits > 0
      ? absolute.toFixed(maximumFractionDigits).replace(/(\.\d*?[1-9])0+$|\.0+$/, "$1")
      : absolute.toFixed(0);
    const [integer, fraction] = fixed.split(".");
    const grouped = integer.replace(/\B(?=(\d{3})+(?!\d))/g, "'");
    return `${numeric < 0 ? "-" : ""}${grouped}${fraction ? `.${fraction}` : ""}`;
  }

  function formatCount(value) {
    const numeric = Number(value) || 0;
    const absolute = Math.abs(numeric);
    const units = [
      { threshold: 1e12, suffix: "T" },
      { threshold: 1e9, suffix: "B" },
      { threshold: 1e6, suffix: "M" },
      { threshold: 1e3, suffix: "K" },
    ];
    const unit = units.find(({ threshold }) => absolute >= threshold);
    if (!unit) return formatNumber(numeric);
    const scaled = numeric / unit.threshold;
    const decimals = Math.abs(scaled) >= 100 ? 0 : Math.abs(scaled) >= 10 ? 1 : 2;
    return `${formatNumber(scaled, decimals)}${unit.suffix}`;
  }

  function utcDate(value) {
    return new Date(`${value}T00:00:00Z`);
  }

  function dateKey(date) {
    return date.toISOString().slice(0, 10);
  }

  function addDays(date, count) {
    const copy = new Date(date);
    copy.setUTCDate(copy.getUTCDate() + count);
    return copy;
  }

  function startOfWeek(date) {
    return addDays(date, -date.getUTCDay());
  }

  function generatedDay(report) {
    return utcDate(OpenCDXTelemetryRanges.dayKey(report.generated_at, report.time_zone));
  }

  function positionTooltip(clientX, clientY) {
    const margin = 12;
    const bounds = tooltip.getBoundingClientRect();
    tooltip.style.left = `${Math.max(margin, Math.min(clientX + 14, window.innerWidth - bounds.width - margin))}px`;
    tooltip.style.top = `${Math.max(margin, Math.min(clientY + 14, window.innerHeight - bounds.height - margin))}px`;
  }

  function showTooltip(title, rows, total, clientX, clientY) {
    tooltip.textContent = "";
    const heading = document.createElement("div");
    heading.className = "tooltip-title";
    heading.textContent = title;
    tooltip.appendChild(heading);
    rows.forEach((row) => {
      const line = document.createElement("div");
      line.className = row.color ? "tooltip-row" : "tooltip-plain";
      if (row.color) {
        const swatch = document.createElement("i");
        swatch.style.background = row.color;
        const copy = document.createElement("div");
        copy.className = "tooltip-copy";
        const label = document.createElement("div");
        label.className = "tooltip-label";
        label.textContent = row.label;
        const value = document.createElement("div");
        value.className = "tooltip-value";
        value.textContent = row.value;
        copy.append(label, value);
        if (row.secondary) {
          const secondary = document.createElement("div");
          secondary.className = "tooltip-secondary";
          secondary.textContent = row.secondary;
          copy.appendChild(secondary);
        }
        line.append(swatch, copy);
      } else {
        line.textContent = row.value;
      }
      tooltip.appendChild(line);
    });
    if (total) {
      const line = document.createElement("div");
      line.className = "tooltip-total";
      const label = document.createElement("span");
      label.textContent = "Total";
      const value = document.createElement("span");
      value.className = "tooltip-total-value";
      value.textContent = total;
      line.append(label, value);
      tooltip.appendChild(line);
    }
    tooltip.classList.add("visible");
    positionTooltip(clientX, clientY);
  }

  function showAnchoredTooltip(element, title, rows, total) {
    const bounds = element.getBoundingClientRect();
    showTooltip(title, rows, total, bounds.right, bounds.top);
  }

  function hideTooltip() {
    tooltip.classList.remove("visible");
  }

  window.addEventListener("scroll", hideTooltip, true);
  window.addEventListener("resize", hideTooltip);

  function seriesKey(point, grouping) {
    if (grouping === "provider") return point.provider || "unknown";
    if (grouping === "routing") return point.routing === "routed" ? "routed" : "native";
    return point.model;
  }

  function seriesLabel(key, grouping) {
    if (grouping === "routing") return key === "routed" ? "Routed" : "Native";
    if (grouping === "provider") {
      return { openai: "OpenAI", openrouter: "OpenRouter", ollama: "Ollama", "claude-code": "Claude Code" }[key] || key;
    }
    return key;
  }

  function groupingLabel(grouping) {
    return { model: "Model", provider: "Provider", routing: "Routing" }[grouping] || "Model";
  }

  const chartPalette = ["#4e79a7", "#f28e2b", "#e15759", "#b07aa1", "#edc948", "#9c755f", "#ff9da7", "#59a14f", "#bab0ac", "#3366cc", "#dc3912", "#9467bd"];
  const seriesColors = new Map();

  function prepareSeriesColors(points, grouping) {
    seriesColors.clear();
    const labels = Array.from(new Set(points.map((point) => seriesKey(point, grouping)))).sort((left, right) => left.localeCompare(right));
    labels.forEach((label, index) => seriesColors.set(label, chartPalette[index % chartPalette.length]));
  }

  function colorFor(key) {
    return seriesColors.get(key) || chartPalette[0];
  }

  function setMetrics(points) {
    const models = new Set(points.map((point) => point.model));
    const totals = points.reduce((sum, point) => {
      sum.requests += point.requests;
      sum.tokens += point.input_tokens + point.output_tokens;
      return sum;
    }, { requests: 0, tokens: 0 });
    telemetryRoot.querySelector('[data-metric="requests"]').textContent = formatCount(totals.requests);
    telemetryRoot.querySelector('[data-metric="tokens"]').textContent = formatCount(totals.tokens);
    telemetryRoot.querySelector('[data-metric="models"]').textContent = formatCount(models.size);
  }

  function renderBreakdown(points, mode, grouping) {
    const host = telemetryRoot.querySelector("[data-model-breakdown]");
    const totalLabel = telemetryRoot.querySelector("[data-breakdown-total]");
    const title = telemetryRoot.querySelector("[data-breakdown-title]");
    const values = new Map();
    points.forEach((point) => {
      const value = mode === "requests" ? point.requests : point.input_tokens + point.output_tokens;
      const key = seriesKey(point, grouping);
      values.set(key, (values.get(key) || 0) + value);
    });
    const ordered = Array.from(values.entries()).sort((left, right) => right[1] - left[1]);
    const total = ordered.reduce((sum, [, value]) => sum + value, 0);
    host.textContent = "";
    title.textContent = `${groupingLabel(grouping)} breakdown`;
    totalLabel.textContent = `${formatCount(total)} ${mode}`;
    if (!ordered.length || total === 0) {
      const empty = document.createElement("li");
      empty.className = "breakdown-empty";
      empty.textContent = `No ${mode} reported in this range.`;
      host.appendChild(empty);
      return;
    }

    const visible = ordered.map(([key, value]) => ({ key, label: seriesLabel(key, grouping), value }));
    visible.forEach((item) => {
      const row = document.createElement("li");
      const label = document.createElement("span");
      label.className = "model-name";
      label.textContent = item.label;
      const share = document.createElement("span");
      share.className = "share";
      share.textContent = formatCount(item.value);
      const microbar = document.createElement("span");
      microbar.className = "microbar";
      const fill = document.createElement("span");
      fill.style.width = `${Math.max(1, (item.value / total) * 100)}%`;
      fill.style.background = item.key ? colorFor(item.key) : "#8f96a3";
      microbar.appendChild(fill);
      row.append(label, share, microbar);
      host.appendChild(row);
    });
  }

  const timelineFormats = new Map();
  function timelineFormat(options) {
    const key = `${selectedTimeZone}:${JSON.stringify(options)}`;
    if (!timelineFormats.has(key)) timelineFormats.set(key, new Intl.DateTimeFormat(undefined, { timeZone: selectedTimeZone, ...options }));
    return timelineFormats.get(key);
  }

  const unitLabels = { hour: "hourly", day: "daily", week: "weekly", month: "monthly" };
  function updateChartMeta(view, unit, mode, grouping) {
    const format = timelineFormat({ month: "short", day: "numeric", year: "numeric", hour: "2-digit", minute: "2-digit" });
    telemetryRoot.querySelector("[data-chart-title]").textContent = `${groupingLabel(grouping)} usage by ${mode}`;
    telemetryRoot.querySelector("[data-chart-meta]").textContent = `${format.format(view.from)} – ${format.format(view.to)} · ${unitLabels[unit]} bars · ${selectedTimeZone} · ${mode === "tokens" ? "input and output combined" : "inference calls"}`;
  }

  function exportTelemetry(points, view) {
    const columns = ["date", "at", "device_id", "device_name", "provider", "model", "source", "routing", "requests", "input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens"];
    const escapeCell = (value) => {
      const text = String(value ?? "");
      return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
    };
    const rows = [columns.join(","), ...points.map((point) => columns.map((column) => escapeCell(point[column])).join(","))];
    const blob = new Blob([`${rows.join("\n")}\n`], { type: "text/csv;charset=utf-8" });
    const link = document.createElement("a");
    const url = URL.createObjectURL(blob);
    link.href = url;
    const day = (at) => OpenCDXTelemetryRanges.dayKey(at, selectedTimeZone);
    link.download = `opencdx-telemetry-${day(view.from)}-${day(view.to - 1)}.csv`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  function renderHeatmap(report) {
    const host = telemetryRoot.querySelector("[data-heatmap]");
    host.textContent = "";
    const counts = new Map(report.activity.map((point) => [point.date, point.requests]));
    const today = generatedDay(report);
    const firstWeek = addDays(startOfWeek(today), -52 * 7);
    const visibleCounts = Array.from(counts.entries()).filter(([date]) => date >= dateKey(firstWeek)).map(([, requests]) => requests);
    const maximum = Math.max(0, ...visibleCounts);
    const scroll = document.createElement("div");
    scroll.className = "heatmap-scroll";
    const layout = document.createElement("div");
    layout.className = "heatmap-layout";
    const months = document.createElement("div");
    months.className = "heatmap-months";
    let previousMonth = -1;
    for (let week = 0; week < 53; week += 1) {
      const date = addDays(firstWeek, week * 7);
      const month = date.getUTCMonth();
      if (month !== previousMonth) {
        const label = document.createElement("span");
        label.style.gridColumn = String(week + 1);
        label.textContent = date.toLocaleDateString(undefined, { month: "short", timeZone: "UTC" });
        months.appendChild(label);
        previousMonth = month;
      }
    }
    const dayLabels = document.createElement("div");
    dayLabels.className = "heatmap-days";
    ["", "Mon", "", "Wed", "", "Fri", ""].forEach((name) => {
      const label = document.createElement("span");
      label.textContent = name;
      dayLabels.appendChild(label);
    });
    const grid = document.createElement("div");
    grid.className = "heatmap-grid";
    for (let week = 0; week < 53; week += 1) {
      for (let day = 0; day < 7; day += 1) {
        const date = addDays(firstWeek, week * 7 + day);
        const key = dateKey(date);
        const requests = counts.get(key) || 0;
        const cell = document.createElement("span");
        cell.className = "activity-cell";
        if (date > today) cell.classList.add("future");
        const level = requests === 0 || maximum === 0 ? 0 : Math.max(1, Math.ceil((Math.log1p(requests) / Math.log1p(maximum)) * 4));
        cell.dataset.level = String(level);
        const description = `${fullDate.format(date)}: ${formatNumber(requests)} inference request${requests === 1 ? "" : "s"}`;
        cell.setAttribute("role", "img");
        cell.setAttribute("aria-label", description);
        if (date <= today) {
          const rows = [{ value: `${formatNumber(requests)} inference request${requests === 1 ? "" : "s"}` }];
          cell.addEventListener("pointerenter", (event) => showTooltip(fullDate.format(date), rows, "", event.clientX, event.clientY));
          cell.addEventListener("pointermove", (event) => positionTooltip(event.clientX, event.clientY));
          cell.addEventListener("pointerleave", hideTooltip);
        }
        grid.appendChild(cell);
      }
    }
    layout.append(months, dayLabels, grid);
    const key = document.createElement("div");
    key.className = "heatmap-key";
    key.innerHTML = "<span>Less</span><i></i><i></i><i></i><i></i><i></i><span>More</span>";
    const content = document.createElement("div");
    content.className = "heatmap-content";
    content.append(layout, key);
    scroll.appendChild(content);
    host.appendChild(scroll);
  }

  function bucketText(bucket, unit) {
    if (unit === "hour") {
      const midnight = OpenCDXTelemetryRanges.dayKey(bucket.from, selectedTimeZone) !== OpenCDXTelemetryRanges.dayKey(bucket.from - 1, selectedTimeZone);
      return {
        label: midnight ? timelineFormat({ month: "short", day: "numeric" }).format(bucket.from) : timelineFormat({ hour: "2-digit", minute: "2-digit" }).format(bucket.from),
        tooltip: timelineFormat({ weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(bucket.from),
      };
    }
    if (unit === "day") {
      return {
        label: timelineFormat({ month: "short", day: "numeric" }).format(bucket.from),
        tooltip: timelineFormat({ weekday: "short", year: "numeric", month: "long", day: "numeric" }).format(bucket.from),
      };
    }
    if (unit === "week") {
      return {
        label: timelineFormat({ month: "short", day: "numeric" }).format(bucket.from),
        tooltip: `Week of ${timelineFormat({ year: "numeric", month: "long", day: "numeric" }).format(bucket.from)}`,
      };
    }
    return {
      label: timelineFormat({ month: "short", year: "2-digit" }).format(bucket.from),
      tooltip: timelineFormat({ month: "long", year: "numeric" }).format(bucket.from),
    };
  }

  function niceMaximum(value) {
    if (value <= 0) return 0;
    const magnitude = 10 ** Math.floor(Math.log10(value));
    const normalized = value / magnitude;
    return (normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10) * magnitude;
  }

  function svgElement(name, attributes = {}) {
    const element = document.createElementNS(svgNS, name);
    Object.entries(attributes).forEach(([key, value]) => element.setAttribute(key, String(value)));
    return element;
  }

  const chartHost = telemetryRoot.querySelector('[data-usage-chart="tokens"]');
  const allowanceToggle = telemetryRoot.querySelector("[data-allowance-toggle]");
  const allowanceWindow = telemetryRoot.querySelector("[data-allowance-window]");
  let allowancePreferences = {};
  try {
    allowancePreferences = JSON.parse(localStorage.getItem("opencdx-allowance-overlay")) || {};
  } catch {}
  let allowanceEnabled = allowancePreferences.enabled === true;
  let allowanceSeconds = Number(allowancePreferences.window) || 604800;
  const hiddenAllowanceAccounts = new Set(Array.isArray(allowancePreferences.hidden) ? allowancePreferences.hidden : []);
  function saveAllowancePreferences() {
    try {
      localStorage.setItem(
        "opencdx-allowance-overlay",
        JSON.stringify({ enabled: allowanceEnabled, window: allowanceSeconds, hidden: [...hiddenAllowanceAccounts] }),
      );
    } catch {}
  }
  allowanceToggle.addEventListener("click", () => {
    allowanceEnabled = !allowanceEnabled;
    saveAllowancePreferences();
    hideTooltip();
    renderTelemetry();
  });
  allowanceWindow.addEventListener("change", () => {
    allowanceSeconds = Number(allowanceWindow.value);
    saveAllowancePreferences();
    hideTooltip();
    renderTelemetry();
  });
  // Provider marks from Simple Icons (CC0); trademarks of OpenAI and Anthropic.
  const providerMarks = {
    openai: "M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z",
    "claude-code": "m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z",
  };
  function providerMark(provider) {
    const svg = svgElement("svg", { viewBox: "0 0 24 24", class: "provider-mark", "aria-hidden": "true" });
    svg.appendChild(svgElement("path", { d: providerMarks[provider] || providerMarks.openai }));
    return svg;
  }

  function renderAllowanceControls(report, range) {
    const windows = OpenCDXTelemetryAllowance.windows(report);
    if (windows.length && !windows.some((w) => w.seconds === allowanceSeconds)) allowanceSeconds = windows[0].seconds;
    const signature = JSON.stringify(windows);
    if (allowanceWindow.dataset.options !== signature) {
      allowanceWindow.replaceChildren(...windows.map((w) => new Option(w.label, w.seconds)));
      allowanceWindow.dataset.options = signature;
    }
    allowanceWindow.value = String(allowanceSeconds);
    allowanceToggle.setAttribute("aria-pressed", String(allowanceEnabled));
    telemetryRoot.querySelector("[data-allowance-window-control]").hidden = !allowanceEnabled || !windows.length;
    allowanceToggle.textContent = allowanceEnabled ? "Hide allowances" : "Show allowances";
    const series = OpenCDXTelemetryAllowance.select(report, range, allowanceSeconds);
    const legend = telemetryRoot.querySelector("[data-allowance-legend]");
    legend.hidden = !allowanceEnabled || !series.length;
    const legendSignature = JSON.stringify(series.map((s) => [s.account_id, s.provider, s.label, s.color, hiddenAllowanceAccounts.has(s.account_id)]));
    if (legend.dataset.accounts !== legendSignature) {
      const focused = legend.contains(document.activeElement) ? document.activeElement.dataset.account : null;
      legend.replaceChildren(
        ...series.map((account) => {
          const button = document.createElement("button");
          button.type = "button";
          button.dataset.account = account.account_id;
          button.setAttribute("aria-pressed", String(!hiddenAllowanceAccounts.has(account.account_id)));
          const swatch = document.createElement("i");
          swatch.style.setProperty("--allowance-color", account.color);
          swatch.setAttribute("aria-hidden", "true");
          // Masked emails can coincide. The short internal ID distinguishes them without exposing credentials.
          const duplicates = series.filter((s) => s.label === account.label).length > 1;
          const label = (account.label || "Account").replace(/^Claude · /, "");
          button.append(
            swatch,
            providerMark(account.provider),
            document.createTextNode(`${label}${duplicates ? ` · ${account.account_id.slice(-6)}` : ""}`),
          );
          button.addEventListener("click", () => {
            if (hiddenAllowanceAccounts.has(account.account_id)) hiddenAllowanceAccounts.delete(account.account_id);
            else hiddenAllowanceAccounts.add(account.account_id);
            saveAllowancePreferences();
            hideTooltip();
            renderTelemetry();
          });
          return button;
        }),
      );
      legend.dataset.accounts = legendSignature;
      if (focused) [...legend.children].find((b) => b.dataset.account === focused)?.focus();
    }
    const visible = series.filter((s) => !hiddenAllowanceAccounts.has(s.account_id));
    return allowanceEnabled ? visible : [];
  }

  function renderUsageChart(view, report, mode, grouping) {
    const host = chartHost;
    host.textContent = "";
    const cyclesHost = telemetryRoot.querySelector("[data-cycle-details]");
    cyclesHost.replaceChildren();
    const timeZone = report.time_zone || "UTC";
    const unit = OpenCDXTelemetryTimeline.unitFor(view.to - view.from);
    const range = { from: new Date(view.from), to: new Date(view.to) };
    const resets = OpenCDXTelemetryRanges.resets(report, range);
    const allowanceSeries = renderAllowanceControls(report, range);
    const timeline = view;
    const aggregated = OpenCDXTelemetryTimeline.aggregate(report, view, unit, timeZone,
      (point) => seriesKey(point, grouping),
      (point) => (mode === "requests" ? point.requests : point.input_tokens + point.output_tokens));
    const bucketList = aggregated.buckets.map((bucket) => ({ ...bucket, ...bucketText(bucket, unit) }));
    const seriesTotals = new Map();
    bucketList.forEach((bucket) => bucket.series.forEach((value, key) => seriesTotals.set(key, (seriesTotals.get(key) || 0) + value)));
    const orderedSeries = Array.from(seriesTotals.keys()).sort((left, right) => {
      const valueDifference = (seriesTotals.get(right) || 0) - (seriesTotals.get(left) || 0);
      if (valueDifference !== 0) return valueDifference;
      return seriesLabel(left, grouping).localeCompare(seriesLabel(right, grouping));
    });
    let maximum = niceMaximum(
      Math.max(0, ...bucketList.map((bucket) => Array.from(bucket.series.values()).reduce((sum, value) => sum + value, 0))),
    );
    const empty = (orderedSeries.length === 0 || maximum === 0) && resets.length === 0 && !allowanceSeries.some((s) => s.points.length);

    maximum = Math.max(1, maximum);
    const width = Math.max(560, host.clientWidth || 960);
    const height = 330;
    const left = 78;
    const right = allowanceEnabled ? 76 : 20;
    const top = 18;
    const bottom = 58;
    const plotWidth = width - left - right;
    const plotHeight = height - top - bottom;
    telemetryState.chart = { width, left, plotWidth };
    const svg = svgElement("svg", {
      viewBox: `0 0 ${width} ${height}`,
      role: "img",
      "aria-label": `Stacked ${grouping} ${mode} usage chart. Drag, swipe, or use arrow keys to move through time; pinch, Control-scroll, or plus and minus keys to zoom.`,
    });
    const clipID = "telemetry-plot-clip";
    const defs = svgElement("defs");
    const clip = svgElement("clipPath", { id: clipID });
    clip.appendChild(svgElement("rect", { x: left, y: 0, width: plotWidth, height: top + plotHeight }));
    defs.appendChild(clip);
    svg.appendChild(defs);
    const plot = svgElement("g", { "clip-path": `url(#${clipID})` });
    for (let tick = 0; tick <= 4; tick += 1) {
      const value = (maximum / 4) * tick;
      const y = top + plotHeight - (plotHeight * tick) / 4;
      svg.appendChild(svgElement("line", { x1: left, x2: width - right, y1: y, y2: y, class: "grid-line" }));
      const label = svgElement("text", { x: left - 10, y: y + 4, "text-anchor": "end" });
      label.textContent = formatCount(value);
      svg.appendChild(label);
    }
    svg.appendChild(svgElement("line", { x1: left, x2: width - right, y1: top + plotHeight, y2: top + plotHeight, class: "axis-line" }));
    svg.appendChild(plot);
    if (empty) {
      const note = svgElement("text", { x: left + plotWidth / 2, y: top + plotHeight / 2, "text-anchor": "middle", class: "chart-empty" });
      note.textContent = "No usage in this period.";
      svg.appendChild(note);
    }
    const timeX = (at) => left + ((at - timeline.from) / (timeline.to - timeline.from)) * plotWidth;
    bucketList.forEach((bucket) => {
      bucket.x = timeX(bucket.from);
      bucket.width = timeX(bucket.to) - bucket.x;
    });
    const allowanceRows = (at) =>
      allowanceSeries
        .map((series) => {
          const point = OpenCDXTelemetryAllowance.nearest(series.points, at);
          return point
            ? {
                color: series.color,
                label: `${series.label} · ${series.window_label}`,
                value: `${formatNumber(point.remaining, 1)}% remaining`,
                secondary: `Account-wide · observed ${new Intl.DateTimeFormat(undefined, { timeZone: report.time_zone, dateStyle: "medium", timeStyle: "short" }).format(new Date(point.at))}`,
              }
            : null;
        })
        .filter(Boolean);
    const cursor = svgElement("line", { class: "allowance-cursor", y1: top, y2: top + plotHeight, visibility: "hidden" });
    // Label a stable subset of buckets so labels do not jump while panning.
    const labelEvery = Math.max(1, Math.ceil(bucketList.length / 12));
    const unitLength = { hour: 3600000, day: 86400000, week: 7 * 86400000, month: 30.44 * 86400000 }[unit];
    bucketList.forEach((bucket) => {
      const slot = bucket.width;
      const barWidth = Math.max(1, Math.min(48, slot * 0.72));
      const x = bucket.x + (slot - barWidth) / 2;
      let stacked = 0;
      orderedSeries.forEach((key) => {
        const value = bucket.series.get(key) || 0;
        if (value <= 0) return;
        const segmentHeight = (value / maximum) * plotHeight;
        const y = top + plotHeight - stacked - segmentHeight;
        plot.appendChild(svgElement("rect", { x, y, width: barWidth, height: Math.max(segmentHeight, 0.6), fill: colorFor(key) }));
        stacked += segmentHeight;
      });
      const values = orderedSeries
        .filter((key) => bucket.series.has(key))
        .map((key) => {
          const value = bucket.series.get(key) || 0;
          const cached = bucket.cached.get(key) || 0;
          return {
            color: colorFor(key),
            label: seriesLabel(key, grouping),
            numeric: value,
            cached,
          };
        })
        .sort((a, b) => b.numeric - a.numeric);
      const total = values.reduce((sum, row) => sum + row.numeric, 0);
      const cachedTotal = values.reduce((sum, row) => sum + row.cached, 0);
      const hitLeft = Math.max(bucket.x, left);
      const hit = svgElement("rect", {
        x: hitLeft,
        y: top,
        width: Math.max(Math.min(bucket.x + slot, left + plotWidth) - hitLeft, 1),
        height: plotHeight,
        class: "bar-hit",
        tabindex: values.length || allowanceSeries.length ? 0 : -1,
        "aria-label": `${bucket.tooltip}, ${formatNumber(total)} ${mode}`,
      });
      if (values.length || allowanceSeries.length) {
        const tooltipRows = values.slice(0, 5).map(({ color, label, numeric, cached }) => ({
          color,
          label,
          value: `${formatNumber(numeric)} ${mode}`,
          secondary: mode === "tokens" && cached > 0 ? `${formatNumber(cached)} cached` : "",
        }));
        if (values.length > 5) {
          const remainder = values.slice(5);
          const remainderValue = remainder.reduce((sum, row) => sum + row.numeric, 0);
          const remainderCached = remainder.reduce((sum, row) => sum + row.cached, 0);
          tooltipRows.push({
            color: "#8f96a3",
            label: `Other · ${remainder.length} group${remainder.length === 1 ? "" : "s"}`,
            value: `${formatNumber(remainderValue)} ${mode}`,
            secondary: mode === "tokens" && remainderCached > 0 ? `${formatNumber(remainderCached)} cached` : "",
          });
        }
        const totalLabel =
          mode === "tokens"
            ? `${formatNumber(total)} tokens${cachedTotal > 0 ? `\n${formatNumber(cachedTotal)} cached` : ""}`
            : `${formatNumber(total)} requests`;
        const rowsAt = (at) => [...tooltipRows, ...allowanceRows(at)];
        const showAtPointer = (event) => {
          const pointer = new DOMPoint(event.clientX, event.clientY).matrixTransform(svg.getScreenCTM().inverse());
          const px = Math.max(left, Math.min(width - right, pointer.x));
          const at = timeline.from + ((px - left) / plotWidth) * (timeline.to - timeline.from);
          cursor.setAttribute("x1", px);
          cursor.setAttribute("x2", px);
          cursor.setAttribute("visibility", allowanceEnabled ? "visible" : "hidden");
          if (telemetryState.panning) return;
          showTooltip(`${bucket.tooltip} · usage totals`, rowsAt(at), totalLabel, event.clientX, event.clientY);
        };
        hit.addEventListener("pointerenter", showAtPointer);
        hit.addEventListener("pointermove", showAtPointer);
        hit.addEventListener("pointerleave", () => {
          hideTooltip();
          cursor.setAttribute("visibility", "hidden");
        });
        hit.addEventListener("focus", () =>
          showAnchoredTooltip(hit, `${bucket.tooltip} · usage totals`, rowsAt((bucket.from + bucket.to) / 2), totalLabel),
        );
        hit.addEventListener("blur", hideTooltip);
      }
      svg.appendChild(hit);
      const center = x + barWidth / 2;
      if (Math.round(bucket.from / unitLength) % labelEvery === 0 && center >= left && center <= left + plotWidth) {
        const label = svgElement("text", { x: center, y: height - 25, "text-anchor": "middle" });
        label.textContent = bucket.label;
        svg.appendChild(label);
      }
    });
    if (allowanceEnabled) {
      for (let tick = 0; tick <= 4; tick++) {
        const label = svgElement("text", {
          x: width - right + 10,
          y: top + plotHeight - (plotHeight * tick) / 4 + 4,
          class: "allowance-axis",
        });
        label.textContent = `${tick * 25}%`;
        svg.appendChild(label);
      }
      const y = (remaining) => top + plotHeight * (1 - remaining / 100);
      for (const series of allowanceSeries) {
        for (const segment of OpenCDXTelemetryAllowance.segments(series.points)) {
          let path = "";
          segment.forEach((point, index) => {
            const x = timeX(Date.parse(point.at));
            if (index && Date.parse(point.reset_at) - Date.parse(segment[index - 1].reset_at) > 60000)
              path += ` L ${x} ${y(segment[index - 1].remaining)}`;
            path += `${index ? " L" : "M"} ${x} ${y(point.remaining)}`;
          });
          plot.appendChild(
            svgElement("path", { d: path, stroke: series.color, class: "allowance-line", "data-account": series.account_id }),
          );
          if (segment.length === 1)
            plot.appendChild(
              svgElement("circle", {
                cx: timeX(Date.parse(segment[0].at)),
                cy: y(segment[0].remaining),
                r: 2.5,
                fill: series.color,
                class: "allowance-dot",
              }),
            );
        }
      }
      svg.appendChild(cursor);
    }
    const resetDate = (value) =>
      new Intl.DateTimeFormat(undefined, { timeZone: report.time_zone || "UTC", dateStyle: "medium", timeStyle: "short" }).format(
        new Date(value),
      );
    const resetDescription = (reset) => {
      const tokens = reset.usage.reduce((sum, row) => sum + row.tokens, 0);
      const requests = reset.usage.reduce((sum, row) => sum + row.requests, 0);
      const timing = reset.scheduled
        ? `Scheduled boundary ${resetDate(reset.at)}, transition observed ${resetDate(reset.observed_at)}`
        : `Inferred between ${resetDate(reset.after)} and ${resetDate(reset.observed_at)}`;
      const scope =
        reset.source === "history"
          ? "Machine history; account unknown (a switch can look like a reset)"
          : "Account quota; routed usage with known account only";
      return `${reset.label}: ${timing}. First observation: ${formatNumber(reset.observed_remaining)}% remaining. ${formatNumber(tokens)} recorded tokens / ${formatNumber(requests)} requests from ${resetDate(reset.at)} to ${reset.until ? resetDate(reset.until) : "latest telemetry (ongoing)"}. ${scope}. ${reset.usage.some((row) => row.untimed) ? "Incomplete: daily-only usage excluded. " : ""}Inferred boundaries make cycle totals approximate; totals cover the full interval, beyond the chart filter.`;
    };
    const resetBuckets = new Map();
    resets.forEach((reset) => {
      const at = Date.parse(reset.at);
      const bucket = bucketList.find((candidate) => at >= candidate.from && at < candidate.to);
      if (!bucket) return;
      if (!resetBuckets.has(bucket)) resetBuckets.set(bucket, []);
      resetBuckets.get(bucket).push(reset);
    });
    bucketList.forEach((bucket) => {
      const entries = resetBuckets.get(bucket);
      if (!entries) return;
      const x = bucket.x + bucket.width / 2;
      if (x < left || x > left + plotWidth) return;
      const marker = svgElement("g", {
        class: "allowance-reset",
        tabindex: 0,
        role: "button",
        "aria-label": `${entries.length} weekly window transition${entries.length === 1 ? "" : "s"}, ${bucket.tooltip}. Activate for cycle details.`,
      });
      marker.appendChild(svgElement("line", { x1: x, x2: x, y1: top + 14, y2: top + plotHeight }));
      marker.appendChild(svgElement("rect", { x: x - 12, y: top - 8, width: 24, height: 28 }));
      const glyph = svgElement("text", { x, y: top + 9, "text-anchor": "middle" });
      glyph.textContent = entries.length > 1 ? `↻${entries.length}` : "↻";
      marker.appendChild(glyph);
      const title = `${entries.length} weekly window transition${entries.length === 1 ? "" : "s"} · ${report.time_zone || "UTC"}`;
      const rows = entries
        .slice(0, 2)
        .map((reset) => ({
          value: `${reset.label} · ${resetDate(reset.at)} · ${reset.scheduled ? "scheduled transition" : "inferred reset"}${reset.source === "history" ? " (account unknown)" : ""}. ${formatNumber(reset.usage.reduce((sum, row) => sum + row.tokens, 0))} recorded tokens until ${reset.until ? resetDate(reset.until) : "latest telemetry"}. Activate for bounds, requests and attribution.`,
        }));
      if (entries.length > 2) rows.push({ value: `${entries.length - 2} more; activate to see all cycle details.` });
      marker.addEventListener("pointerenter", (event) => showTooltip(title, rows, "", event.clientX, event.clientY));
      marker.addEventListener("pointerleave", hideTooltip);
      marker.addEventListener("focus", () => showAnchoredTooltip(marker, title, rows, ""));
      marker.addEventListener("blur", hideTooltip);
      const expand = () => {
        hideTooltip();
        details.open = true;
        cycleItems.get(entries[0]).focus();
      };
      marker.addEventListener("click", expand);
      marker.addEventListener("keydown", (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          expand();
        }
      });
      svg.appendChild(marker);
    });
    host.appendChild(svg);
    const details = document.createElement("details");
    const cycleItems = new Map();
    if (resets.length) {
      details.className = "allowance-cycle-details";
      const summary = document.createElement("summary");
      summary.textContent = `↻ ${resets.length} weekly window transition${resets.length === 1 ? "" : "s"} · cycle details`;
      const note = document.createElement("p");
      note.textContent = `Times in ${report.time_zone || "UTC"}. Markers group transitions by chart bucket. Historical observations can overlap accounts or machines; this is not a count of unique account resets. Usage is recorded input + output (cached tokens included), not allowance billing.`;
      const list = document.createElement("ul");
      resets.forEach((reset) => {
        const item = document.createElement("li");
        item.tabIndex = -1;
        item.textContent = resetDescription(reset);
        cycleItems.set(reset, item);
        list.appendChild(item);
      });
      details.append(summary, note, list);
      cyclesHost.appendChild(details);
    }
  }

  function earliestUsageDay(report) {
    return utcDate(OpenCDXTelemetryRanges.dayKey(OpenCDXTelemetryTimeline.extent(report).earliest, report.time_zone));
  }

  // The visible window for a preset, or for the custom date inputs.
  function viewForSelection(report, selection) {
    if (selection !== "custom") return OpenCDXTelemetryTimeline.presetView(selection, report);
    const startValue = telemetryRoot.querySelector("[data-range-start]").value;
    const endValue = telemetryRoot.querySelector("[data-range-end]").value;
    if (!startValue || !endValue || startValue > endValue) return null;
    const timeZone = report.time_zone || "UTC";
    const end = dateKey(addDays(utcDate(endValue), 1));
    return OpenCDXTelemetryTimeline.clamp(
      { from: OpenCDXTelemetryAllowance.dayStart(startValue, timeZone), to: OpenCDXTelemetryAllowance.dayStart(end, timeZone) },
      OpenCDXTelemetryTimeline.extent(report),
    );
  }

  function telemetryFailed() {
    telemetryRoot.querySelectorAll("[data-heatmap], [data-usage-chart]").forEach((host) => {
      host.innerHTML = '<div class="telemetry-empty">Telemetry could not be loaded. Reload after confirming the router session is active.</div>';
    });
    const breakdown = telemetryRoot.querySelector("[data-model-breakdown]");
    if (breakdown) breakdown.innerHTML = '<li class="breakdown-empty">Telemetry could not be loaded.</li>';
  }

  const deviceSelect = telemetryRoot.querySelector("[data-telemetry-device]");
  function updateTelemetryDevices(report) {
    const selected = deviceSelect.value;
    deviceSelect.replaceChildren(new Option("All machines", "*"));
    for (const device of OpenCDXTelemetryDevices.devices(report)) {
      deviceSelect.add(new Option(device.name, device.id));
    }
    deviceSelect.value = Array.from(deviceSelect.options).some((option) => option.value === selected) ? selected : "*";
  }
  const select = telemetryRoot.querySelector("[data-telemetry-range]");
  const custom = telemetryRoot.querySelector("[data-custom-range]");
  const startInput = telemetryRoot.querySelector("[data-range-start]");
  const endInput = telemetryRoot.querySelector("[data-range-end]");
  const rangeError = telemetryRoot.querySelector("[data-range-error]");
  const exportButton = telemetryRoot.querySelector("[data-export-telemetry]");
  const presets = Array.from(telemetryRoot.querySelectorAll("[data-range-preset]"));
  const customButton = telemetryRoot.querySelector('[data-range-preset="custom"]');
  const telemetryState = {
    report: null,
    currentPoints: [],
    view: null,
    following: true,
    preset: select.value,
    panning: false,
    chart: null,
    boundsInitialized: false,
    exporting: false,
  };

  const nowButton = telemetryRoot.querySelector("[data-timeline-now]");
  const syncPresetButtons = (active = telemetryState.preset) => {
    presets.forEach((button) => button.classList.toggle("active", button.dataset.rangePreset === active));
  };
  const closeCustomRange = (restoreFocus = false) => {
    custom.hidden = true;
    rangeError.hidden = true;
    customButton.setAttribute("aria-expanded", "false");
    syncPresetButtons();
    if (restoreFocus) customButton.focus();
  };
  const openCustomRange = () => {
    custom.hidden = false;
    customButton.setAttribute("aria-expanded", "true");
    syncPresetButtons("custom");
    window.requestAnimationFrame(() => startInput.focus());
  };

  function updateTelemetryBounds(report) {
    const earliest = dateKey(earliestUsageDay(report));
    const latest = dateKey(generatedDay(report));
    startInput.min = earliest;
    startInput.max = latest;
    endInput.min = earliest;
    endInput.max = latest;
    if (!telemetryState.boundsInitialized) {
      startInput.value = earliest;
      endInput.value = latest;
      telemetryState.boundsInitialized = true;
    }
  }

  // Presets and custom dates choose the visible window; drag, swipe, and
  // zoom move it through all recorded history.
  function applySelection(selection) {
    if (!telemetryState.report) {
      // Remember a choice made while the first report is still loading.
      select.value = selection;
      telemetryState.preset = selection;
      syncPresetButtons();
      return selection !== "custom";
    }
    const view = viewForSelection(telemetryState.report, selection);
    if (!view) {
      rangeError.textContent = "Choose a valid start and end date.";
      rangeError.hidden = false;
      return false;
    }
    rangeError.hidden = true;
    select.value = selection;
    telemetryState.preset = selection;
    telemetryState.view = view;
    telemetryState.following = view.to >= Date.parse(telemetryState.report.generated_at);
    renderTelemetry();
    return true;
  }

  function moveView(view, keepPreset = true) {
    if (!telemetryState.report) return;
    telemetryState.view = view;
    telemetryState.following = view.to >= Date.parse(telemetryState.report.generated_at) - 60000;
    if (!keepPreset) telemetryState.preset = null;
    if (renderFrame) return;
    renderFrame = window.requestAnimationFrame(() => {
      renderFrame = 0;
      renderTelemetry();
    });
  }
  let renderFrame = 0;

  // A refreshed report keeps a window that follows the present moving with it.
  function followReport(report) {
    if (!telemetryState.view) {
      telemetryState.view = viewForSelection(report, select.value) || OpenCDXTelemetryTimeline.presetView("all", report);
      telemetryState.following = true;
      return;
    }
    const span = telemetryState.view.to - telemetryState.view.from;
    const to = telemetryState.following ? Date.parse(report.generated_at) : telemetryState.view.to;
    telemetryState.view = OpenCDXTelemetryTimeline.clamp({ from: to - span, to }, OpenCDXTelemetryTimeline.extent(report));
  }

  function renderTelemetry(includeHeatmap = false) {
    if (!telemetryState.report || !telemetryState.view) return false;
    const report = OpenCDXTelemetryDevices.filter(telemetryState.report, deviceSelect.value);
    const view = telemetryState.view;
    const pageScroll = { x: window.scrollX, y: window.scrollY };
    const heatmapScroll = telemetryRoot.querySelector(".heatmap-scroll")?.scrollLeft || 0;
    syncPresetButtons();
    nowButton.hidden = telemetryState.following;
    const unit = OpenCDXTelemetryTimeline.unitFor(view.to - view.from);
    const selected = OpenCDXTelemetryTimeline.visible(report, view);
    const hiddenUntimed = unit === "hour" && selected.points.some((point) => !point.at);
    const precision = telemetryRoot.querySelector("[data-telemetry-precision]");
    precision.hidden = !selected.partial && !hiddenUntimed;
    precision.textContent = hiddenUntimed
      ? "Some history has only daily totals and cannot be shown as hourly bars. Zoom out to see it, or import usage with the updated helper to restore request timestamps."
      : selected.partial ? "Totals include older daily-only records whose day extends past the visible window." : "";
    const points = selected.points;
    telemetryState.currentPoints = points;
    const mode = telemetryRoot.querySelector("[data-metric-mode]")?.value || "tokens";
    const grouping = telemetryRoot.querySelector("[data-group-mode]")?.value || "model";
    prepareSeriesColors(report.usage, grouping);
    if (includeHeatmap) renderHeatmap(report);
    setMetrics(points);
    renderUsageChart(view, report, mode, grouping);
    renderBreakdown(points, mode, grouping);
    updateChartMeta(view, unit, mode, grouping);
    const nextHeatmap = telemetryRoot.querySelector(".heatmap-scroll");
    if (nextHeatmap) nextHeatmap.scrollLeft = heatmapScroll;
    exportButton.disabled = telemetryState.exporting;
    window.requestAnimationFrame(() => window.scrollTo(pageScroll.x, pageScroll.y));
    return true;
  }

  const plotScreenBounds = () => {
    const chart = telemetryState.chart;
    if (!chart) return null;
    const bounds = chartHost.getBoundingClientRect();
    const scale = bounds.width / chart.width;
    return { left: bounds.left + chart.left * scale, width: chart.plotWidth * scale };
  };
  let drag = null;
  let suppressClickUntil = 0;
  chartHost.addEventListener("click", (event) => {
    if (performance.now() < suppressClickUntil) event.stopPropagation();
  }, { capture: true });
  chartHost.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || !telemetryState.view) return;
    drag = { id: event.pointerId, x: event.clientX, view: telemetryState.view, moved: false };
  });
  chartHost.addEventListener("pointermove", (event) => {
    if (!drag || event.pointerId !== drag.id) return;
    const dx = event.clientX - drag.x;
    if (!drag.moved && Math.abs(dx) < 4) return;
    const plot = plotScreenBounds();
    if (!plot) return;
    if (!drag.moved) {
      drag.moved = true;
      telemetryState.panning = true;
      try { chartHost.setPointerCapture(event.pointerId); } catch { /* The pointer may already be gone. */ }
      chartHost.classList.add("is-panning");
      hideTooltip();
    }
    const span = drag.view.to - drag.view.from;
    moveView(OpenCDXTelemetryTimeline.pan(drag.view, (-dx / plot.width) * span, telemetryState.report));
  });
  const endDrag = (event) => {
    if (!drag || event.pointerId !== drag.id) return;
    if (drag.moved) {
      if (chartHost.hasPointerCapture?.(event.pointerId)) chartHost.releasePointerCapture(event.pointerId);
      chartHost.classList.remove("is-panning");
      // The click that ends a drag must not open a reset marker.
      suppressClickUntil = performance.now() + 100;
      window.setTimeout(() => { telemetryState.panning = false; }, 0);
    }
    drag = null;
  };
  chartHost.addEventListener("pointerup", endDrag);
  chartHost.addEventListener("pointercancel", endDrag);
  const zoomAt = (factor, clientX) => {
    const view = telemetryState.view;
    const plot = plotScreenBounds();
    if (!view || !plot) return;
    const ratio = clientX === undefined ? 1 : Math.min(1, Math.max(0, (clientX - plot.left) / plot.width));
    // Zooming at the right edge keeps a window that follows the present.
    const anchor = telemetryState.following && ratio > 0.9 ? view.to : view.from + ratio * (view.to - view.from);
    moveView(OpenCDXTelemetryTimeline.zoom(view, factor, anchor, telemetryState.report), false);
  };
  chartHost.addEventListener("wheel", (event) => {
    const view = telemetryState.view;
    const plot = plotScreenBounds();
    if (!view || !plot) return;
    if (event.ctrlKey || event.metaKey) {
      event.preventDefault();
      zoomAt(Math.exp(Math.max(-1, Math.min(1, event.deltaY * 0.01))), event.clientX);
    } else if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) {
      // Horizontal swipes move through time; vertical scrolling stays with the page.
      event.preventDefault();
      moveView(OpenCDXTelemetryTimeline.pan(view, (event.deltaX / plot.width) * (view.to - view.from), telemetryState.report));
    }
  }, { passive: false });
  let gesture = null;
  chartHost.addEventListener("gesturestart", (event) => {
    event.preventDefault();
    gesture = { view: telemetryState.view, x: event.clientX };
  });
  chartHost.addEventListener("gesturechange", (event) => {
    if (!gesture?.view) return;
    event.preventDefault();
    telemetryState.view = gesture.view;
    zoomAt(1 / event.scale, gesture.x);
  });
  chartHost.addEventListener("gestureend", () => { gesture = null; });
  chartHost.addEventListener("keydown", (event) => {
    const view = telemetryState.view;
    if (!view || event.altKey || event.ctrlKey || event.metaKey) return;
    const span = view.to - view.from;
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      moveView(OpenCDXTelemetryTimeline.pan(view, (event.key === "ArrowLeft" ? -0.25 : 0.25) * span, telemetryState.report));
    } else if (event.key === "+" || event.key === "=" || event.key === "-") {
      event.preventDefault();
      zoomAt(event.key === "-" ? 1.5 : 1 / 1.5);
    }
  });
  nowButton.addEventListener("click", () => {
    const view = telemetryState.view;
    if (!view || !telemetryState.report) return;
    const to = Date.parse(telemetryState.report.generated_at);
    moveView(OpenCDXTelemetryTimeline.clamp({ from: to - (view.to - view.from), to }, OpenCDXTelemetryTimeline.extent(telemetryState.report)));
  });

  select.addEventListener("change", () => {
    if (select.value === "custom") openCustomRange();
    else {
      closeCustomRange();
      applySelection(select.value);
    }
  });
  presets.forEach((button) => button.addEventListener("click", () => {
    const selection = button.dataset.rangePreset;
    if (selection === "custom") {
      openCustomRange();
      return;
    }
    closeCustomRange();
    applySelection(selection);
  }));
  deviceSelect.addEventListener("change", () => renderTelemetry(true));
  telemetryRoot.querySelector("[data-metric-mode]")?.addEventListener("change", () => renderTelemetry());
  telemetryRoot.querySelector("[data-group-mode]")?.addEventListener("change", () => renderTelemetry());
  telemetryRoot.querySelector("[data-apply-range]").addEventListener("click", () => {
    if (applySelection("custom")) closeCustomRange();
  });
  telemetryRoot.querySelectorAll("[data-close-custom-range]").forEach((button) => {
    button.addEventListener("click", () => closeCustomRange(true));
  });
  [startInput, endInput].forEach((input) => input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && applySelection("custom")) closeCustomRange();
  }));
  document.addEventListener("pointerdown", (event) => {
    if (!custom.hidden && !custom.contains(event.target) && !customButton.contains(event.target)) closeCustomRange();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !custom.hidden) closeCustomRange(true);
  });

  const conditionalHeaders = (accept, etag) => {
    const headers = { Accept: accept };
    if (etag) headers["If-None-Match"] = etag;
    return headers;
  };
  const throwIfAborted = (signal) => {
    if (!signal.aborted) return;
    const error = new Error("request aborted");
    error.name = "AbortError";
    throw error;
  };

  async function refreshTelemetry(signal, etag) {
    const response = await fetch(`/admin/telemetry?timezone=${encodeURIComponent(selectedTimeZone)}`, {
      credentials: "same-origin",
      headers: conditionalHeaders("application/json", etag),
      signal,
    });
    throwIfAborted(signal);
    if (response.status === 304) {
      const serverDate = response.headers.get("X-OpenCDX-Generated-At") || response.headers.get("Date");
      if (telemetryState.report && serverDate) {
        telemetryState.report.generated_at = new Date(serverDate).toISOString();
        updateTelemetryBounds(telemetryState.report);
        followReport(telemetryState.report);
        renderTelemetry(true);
      }
      return { etag: response.headers.get("ETag") || etag };
    }
    if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
      throw new Error("telemetry unavailable");
    }
    const report = await response.json();
    throwIfAborted(signal);
    telemetryState.report = report;
    updateTelemetryDevices(report);
    updateTelemetryBounds(report);
    followReport(report);
    renderTelemetry(true);
    return { etag: response.headers.get("ETag") || "" };
  }

  const devicesLive = document.querySelector("[data-devices-live]");
  let pendingDevicesHTML = null;
  let devicesPointerActive = false;
  const devicesBusy = () => devicesPointerActive || (devicesLive?.contains(document.activeElement) ?? false);
  const applyPendingDevices = () => {
    if (pendingDevicesHTML === null || !devicesLive || devicesBusy()) return;
    devicesLive.innerHTML = pendingDevicesHTML;
    pendingDevicesHTML = null;
    localizeTimes(devicesLive);
  };
  async function refreshDevices(signal, etag) {
    const response = await fetch("/admin/devices/live", {
      credentials: "same-origin",
      headers: conditionalHeaders("text/html", etag),
      signal,
    });
    if (response.status === 304) return { etag: response.headers.get("ETag") || etag };
    if (!response.ok || !response.headers.get("Content-Type")?.includes("text/html")) {
      throw new Error("device state unavailable");
    }
    const html = await response.text();
    throwIfAborted(signal);
    pendingDevicesHTML = html;
    applyPendingDevices();
    return { etag: response.headers.get("ETag") || "" };
  }
  devicesLive?.addEventListener("pointerdown", () => { devicesPointerActive = true; });
  document.addEventListener("pointerup", () => {
    devicesPointerActive = false;
    applyPendingDevices();
  });
  devicesLive?.addEventListener("focusout", () => window.requestAnimationFrame(applyPendingDevices));

  const accountsLive = document.querySelector("[data-accounts-live]");
  let pendingAccounts = null;
  let accountsPointerActive = false;
  const accountsBusy = () => accountsPointerActive
    || accountList?.classList.contains("is-reordering")
    || accountList?.classList.contains("is-saving")
    || (accountsLive?.contains(document.activeElement) ?? false);
  const createLocalizedTime = (value, kind) => {
    const element = document.createElement("time");
    element.dateTime = value;
    if (kind === "date") element.dataset.localDate = "";
    else if (kind === "clock") element.dataset.localClock = "";
    else element.dataset.localDatetime = "";
    localizeTime(element, kind === "date" ? localDate : kind === "clock" ? localClock : localDateTime);
    return element;
  };

  function updateAccountMetrics(report) {
    accountsLive.querySelector("[data-account-connected]").textContent = formatNumber(report.accounts.length);
    accountsLive.querySelector("[data-account-ready]").textContent = formatNumber(report.ready_count);
    accountsLive.querySelector("[data-account-primary-plan]").textContent = report.primary_account_plan || "—";
    accountsLive.querySelector("[data-account-primary-email]").textContent = report.primary_account_email || "No primary account";
    const nearestDate = accountsLive.querySelector("[data-account-nearest-date]");
    const nearestNote = accountsLive.querySelector("[data-account-nearest-note]");
    if (report.nearest_reset_at) {
      nearestDate.replaceChildren(createLocalizedTime(report.nearest_reset_at, "date"));
      nearestNote.replaceChildren(createLocalizedTime(report.nearest_reset_at, "clock"), " local time");
    } else {
      nearestDate.textContent = "—";
      nearestNote.textContent = "No reset reported";
    }
    const health = accountsLive.querySelector("[data-account-health]");
    health.className = `state ${report.healthy ? "good" : "warn"}`;
    health.textContent = report.healthy ? "Ready" : "Needs attention";
  }

  function updateAccountRow(row, account) {
    document.dispatchEvent(new CustomEvent("opencdx:reset-tickets", { detail: { row, account } }));
    row.querySelector("[data-account-name]").textContent = account.masked_email;
    const summary = `${account.primary ? "Primary" : "Fallback"} · ${account.plan} plan`;
    row.querySelector("[data-account-summary]").textContent = summary;
    const status = row.querySelector("[data-account-status]");
    status.className = `state ${account.paused ? "warn" : account.status === "ready" ? "good" : "bad"}`;
    status.textContent = account.paused ? "Paused" : account.status;
    const quotas = account.quotas || [];
    const codex = quotas.find((quota) => quota.name === "Codex");
    const mainCodexWindow = codex?.windows?.[0];
    const reset = row.querySelector("[data-account-reset]");
    if (reset && mainCodexWindow?.reset_at) reset.replaceChildren("Codex resets ", createLocalizedTime(mainCodexWindow.reset_at, "datetime"));
    else if (reset) reset.textContent = "Reset time unavailable";
    const quotaHost = row.querySelector("[data-account-quotas]");
    const quotaItems = quotas.map((quota) => {
      const item = document.createElement("div");
      item.className = "quota-item";
      (quota.windows || []).forEach((window, index) => {
        const windowHost = document.createElement("div");
        windowHost.className = `quota-window${index > 0 ? " quota-window-secondary" : ""}`;
        const head = document.createElement("div");
        head.className = "quota-head";
        const name = document.createElement("span");
        name.textContent = index === 0
          ? `${quota.name}${window.label && window.label !== "Allowance" ? ` · ${window.label}` : ""}`
          : window.label;
        const value = document.createElement("span");
        value.className = "quota-value";
        value.textContent = `${formatNumber(window.remaining)}%`;
        head.append(name, value);
        const track = document.createElement("div");
        track.className = "quota-track";
        track.setAttribute("role", "progressbar");
        track.setAttribute("aria-label", `${quota.name} ${window.label} allowance remaining`);
        track.setAttribute("aria-valuemin", "0");
        track.setAttribute("aria-valuemax", "100");
        track.setAttribute("aria-valuenow", String(Math.round(window.remaining)));
        const fill = document.createElement("span");
        fill.className = "quota-fill";
        fill.style.width = `${Math.max(0, Math.min(100, window.remaining))}%`;
        track.appendChild(fill);
        if (window.pace_status) {
          const marker = document.createElement("i");
          marker.className = "quota-pace-marker";
          marker.style.left = `${Math.max(0, Math.min(100, window.pace_marker_percent))}%`;
          marker.setAttribute("aria-hidden", "true");
          track.appendChild(marker);
        }
        windowHost.append(head, track);
        if (window.pace_status || (index > 0 && window.reset_at)) {
          const meta = document.createElement("div");
          meta.className = "quota-meta";
          if (window.pace_status) {
            const pace = document.createElement("span");
            pace.className = `quota-pace${window.pace_status === "too_fast" ? " too-fast" : ""}`;
            if (window.pace_status === "too_fast") {
              pace.textContent = `Going fast · ${formatNumber(Math.abs(window.pace_buffer_percent))}% behind`;
            } else {
              pace.textContent = window.pace_buffer_percent > 0.49
                ? `On pace · ${formatNumber(window.pace_buffer_percent)}% buffer`
                : "On pace";
            }
            meta.appendChild(pace);
          }
          if (index > 0 && window.reset_at) {
            const resetCopy = document.createElement("span");
            resetCopy.append("Resets ", createLocalizedTime(window.reset_at, "datetime"));
            meta.appendChild(resetCopy);
          }
          windowHost.appendChild(meta);
        }
        item.appendChild(windowHost);
      });
      return item;
    });
    if (quotaItems.length === 0) {
      const empty = document.createElement("span");
      empty.className = "muted";
      empty.textContent = "Allowance unavailable.";
      quotaItems.push(empty);
    }
    quotaHost.replaceChildren(...quotaItems);
    const error = row.querySelector("[data-account-error]");
    error.textContent = account.last_error || "";
    error.hidden = !account.last_error;
    const pauseForm = row.querySelector("[data-account-pause-form]");
    const pauseButton = row.querySelector("[data-account-pause-button]");
    const pauseAction = account.paused ? "resume" : "pause";
    const pauseLabel = account.paused ? "Resume routing" : "Pause routing";
    pauseForm.action = `/admin/accounts/${encodeURIComponent(account.id)}/${pauseAction}`;
    pauseButton.setAttribute("aria-label", pauseLabel);
    pauseButton.title = pauseLabel;
    pauseButton.querySelector(".material-symbols-outlined").textContent = account.paused ? "play_arrow" : "pause";
  }

  function applyPendingAccounts() {
    if (!pendingAccounts || !accountsLive || accountsBusy()) return;
    const report = pendingAccounts;
    const rows = Array.from(accountsLive.querySelectorAll("[data-account-id]"));
    const structureChanged = rows.length !== report.accounts.length || rows.some((row, index) => {
      const account = report.accounts[index];
      return !account || row.dataset.accountId !== account.id || row.dataset.accountEmail !== account.masked_email
        || (row.dataset.accountPrimary === "true") !== account.primary;
    });
    pendingAccounts = null;
    if (structureChanged) {
      window.location.reload();
      return;
    }
    updateAccountMetrics(report);
    rows.forEach((row, index) => updateAccountRow(row, report.accounts[index]));
  }

  async function refreshAccounts(signal, etag) {
    const response = await fetch("/admin/accounts/live", {
      credentials: "same-origin",
      headers: conditionalHeaders("application/json", etag),
      signal,
    });
    if (response.status === 304) return { etag: response.headers.get("ETag") || etag };
    if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
      throw new Error("account state unavailable");
    }
    const report = await response.json();
    throwIfAborted(signal);
    pendingAccounts = report;
    applyPendingAccounts();
    return { etag: response.headers.get("ETag") || "" };
  }
  document.addEventListener("opencdx:reset-completed", async () => {
    const state = liveStates.accounts;
    state.controller?.abort();
    await state.promise;
    pendingAccounts = null;
    state.etag = "";
    await runLiveRefresh("accounts", state);
  });
  accountsLive?.addEventListener("pointerdown", () => { accountsPointerActive = true; });
  document.addEventListener("pointerup", () => {
    accountsPointerActive = false;
    applyPendingAccounts();
  });
  accountsLive?.addEventListener("focusout", () => window.requestAnimationFrame(applyPendingAccounts));
  accountList?.addEventListener("dragend", () => window.requestAnimationFrame(applyPendingAccounts));

  const liveStates = {
    home: { interval: 5000, maxDelay: 30000, etag: "", failures: 0, timer: null, controller: null, promise: null, succeeded: false, refresh: refreshTelemetry, initialError: telemetryFailed },
    devices: { interval: 2500, maxDelay: 20000, etag: "", failures: 0, timer: null, controller: null, promise: null, succeeded: false, refresh: refreshDevices },
    accounts: { interval: 20000, maxDelay: 120000, etag: "", failures: 0, timer: null, controller: null, promise: null, succeeded: false, refresh: refreshAccounts },
  };
  const timeZoneSelect = telemetryRoot.querySelector("[data-telemetry-timezone]");
  const detectedTimeZone = OpenCDXTelemetryRanges.preferredTimeZone("", browserTimeZone, fallbackTimeZone);
  timeZoneSelect.add(new Option(`Automatic (${detectedTimeZone})`, ""));
  const timeZones = new Set(["UTC", selectedTimeZone, detectedTimeZone,
    "America/Argentina/Buenos_Aires", "Europe/London",
    ...(Intl.supportedValuesOf?.("timeZone") || [])]);
  Array.from(timeZones).sort().forEach((zone) => timeZoneSelect.add(new Option(zone.replaceAll("_", " "), zone)));
  timeZoneSelect.value = savedTimeZone ? selectedTimeZone : "";
  timeZoneSelect.addEventListener("change", async () => {
    timeZoneSelect.disabled = true;
    const state = liveStates.home;
    state.controller?.abort();
    await state.promise;
    window.clearTimeout(state.timer);
    state.timer = null;
    selectedTimeZone = timeZoneSelect.value || detectedTimeZone;
    try {
      if (timeZoneSelect.value) localStorage.setItem("opencdx-timezone", selectedTimeZone);
      else localStorage.removeItem("opencdx-timezone");
    } catch {}
    configureTimeFormats();
    localizeTimes();
    state.etag = "";
    state.succeeded = false;
    telemetryState.report = null;
    telemetryState.view = null;
    telemetryState.chart = null;
    exportButton.disabled = true;
    telemetryRoot.setAttribute("aria-busy", "true");
    telemetryRoot.querySelectorAll("[data-metric]").forEach((element) => { element.textContent = "—"; });
    telemetryRoot.querySelector('[data-usage-chart="tokens"]').textContent = "Loading usage…";
    telemetryRoot.querySelector("[data-heatmap]").textContent = "";
    telemetryRoot.querySelector("[data-model-breakdown]").textContent = "";
    telemetryRoot.querySelector("[data-breakdown-total]").textContent = "";
    telemetryRoot.querySelector("[data-chart-meta]").textContent = selectedTimeZone;
    await runLiveRefresh("home", state);
    telemetryRoot.removeAttribute("aria-busy");
    timeZoneSelect.disabled = false;
  });
  let windowFocused = document.hasFocus?.() ?? true;
  const liveStateActive = (name) => selectedTab === name && document.visibilityState !== "hidden" && windowFocused;

  function scheduleLiveRefresh(name, state, delay) {
    window.clearTimeout(state.timer);
    state.timer = window.setTimeout(() => {
      state.timer = null;
      if (liveStateActive(name)) runLiveRefresh(name, state);
    }, delay);
  }

  function runLiveRefresh(name, state) {
    if (state.promise) return state.promise;
    window.clearTimeout(state.timer);
    state.timer = null;
    const controller = new AbortController();
    state.controller = controller;
    const operation = (async () => {
      try {
        const result = await state.refresh(controller.signal, state.etag);
        if (result?.etag) state.etag = result.etag;
        state.failures = 0;
        state.succeeded = true;
      } catch (error) {
        if (error?.name !== "AbortError") {
          state.failures += 1;
          if (!state.succeeded) state.initialError?.();
        }
      } finally {
        if (state.promise === operation) state.promise = null;
        if (state.controller === controller) state.controller = null;
        if (liveStateActive(name)) {
          const backoff = Math.min(state.maxDelay, state.interval * (2 ** Math.min(state.failures, 4)));
          scheduleLiveRefresh(name, state, controller.signal.aborted ? 0 : backoff);
        }
      }
    })();
    state.promise = operation;
    return operation;
  }

  function syncLiveRefresh(immediate = false) {
    Object.entries(liveStates).forEach(([name, state]) => {
      if (!liveStateActive(name)) {
        window.clearTimeout(state.timer);
        state.timer = null;
        state.controller?.abort();
        return;
      }
      if (immediate) {
        window.clearTimeout(state.timer);
        state.timer = null;
      }
      if (!state.promise && (immediate || state.timer === null)) runLiveRefresh(name, state);
    });
  }

  exportButton.addEventListener("click", async () => {
    telemetryState.exporting = true;
    exportButton.disabled = true;
    try {
      await runLiveRefresh("home", liveStates.home);
      if (telemetryState.view) exportTelemetry(telemetryState.currentPoints, telemetryState.view);
    } finally {
      telemetryState.exporting = false;
      exportButton.disabled = !telemetryState.report;
    }
  });

  document.addEventListener("visibilitychange", () => {
    windowFocused = document.visibilityState !== "hidden" && (document.hasFocus?.() ?? true);
    syncLiveRefresh(windowFocused);
  });
  window.addEventListener("focus", () => {
    windowFocused = true;
    syncLiveRefresh(true);
  });
  window.addEventListener("blur", () => {
    windowFocused = false;
    syncLiveRefresh(false);
  });
  refreshCoordinator = syncLiveRefresh;
  syncLiveRefresh(true);
})();
