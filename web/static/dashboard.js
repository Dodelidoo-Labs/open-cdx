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

  const tabNames = ["home", "accounts", "providers", "devices", "catalog"];
  const tabTitles = {
    home: "Telemetry",
    accounts: "OpenAI accounts",
    providers: "Providers",
    devices: "Devices",
    catalog: "Catalog",
  };
  const tabs = Array.from(document.querySelectorAll("[data-tab]"));
  const panels = Array.from(document.querySelectorAll("[data-tab-panel]"));
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

  document.querySelectorAll("form[data-confirm]").forEach((form) => {
    form.addEventListener("submit", (event) => {
      if (!window.confirm(form.dataset.confirm)) event.preventDefault();
    });
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
      button.setAttribute("aria-label", "Refreshing quotas and catalogs");
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
    const parsed = new Date(report.generated_at);
    return new Date(Date.UTC(parsed.getUTCFullYear(), parsed.getUTCMonth(), parsed.getUTCDate()));
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
      return { openai: "OpenAI", openrouter: "OpenRouter", ollama: "Ollama" }[key] || key;
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

  function pointsForRange(report, range) {
    const start = dateKey(range.start);
    const end = dateKey(range.end);
    return report.usage.filter((point) => point.date >= start && point.date <= end);
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

  function updateChartMeta(range, mode, grouping) {
    const date = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" });
    const start = date.format(range.start);
    const end = date.format(range.end);
    telemetryRoot.querySelector("[data-chart-title]").textContent = `${groupingLabel(grouping)} usage by ${mode}`;
    telemetryRoot.querySelector("[data-chart-meta]").textContent = `${start === end ? start : `${start}–${end}`} · ${mode === "tokens" ? "input and output combined" : "inference calls"}`;
  }

  function exportTelemetry(points, range) {
    const columns = ["date", "provider", "model", "source", "routing", "requests", "input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens"];
    const escapeCell = (value) => {
      const text = String(value ?? "");
      return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
    };
    const rows = [columns.join(","), ...points.map((point) => columns.map((column) => escapeCell(point[column])).join(","))];
    const blob = new Blob([`${rows.join("\n")}\n`], { type: "text/csv;charset=utf-8" });
    const link = document.createElement("a");
    const url = URL.createObjectURL(blob);
    link.href = url;
    link.download = `opencdx-telemetry-${dateKey(range.start)}-${dateKey(range.end)}.csv`;
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

  function bucketDetails(date, spanDays) {
    if (spanDays <= 45) {
      return {
        key: dateKey(date),
        label: date.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" }),
        tooltip: fullDate.format(date),
      };
    }
    if (spanDays <= 180) {
      const week = startOfWeek(date);
      return {
        key: dateKey(week),
        label: week.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" }),
        tooltip: `Week of ${fullDate.format(week)}`,
      };
    }
    if (spanDays <= 3650) {
      const key = `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, "0")}`;
      return {
        key,
        label: date.toLocaleDateString(undefined, { month: "short", year: "2-digit", timeZone: "UTC" }),
        tooltip: date.toLocaleDateString(undefined, { month: "long", year: "numeric", timeZone: "UTC" }),
      };
    }
    const year = String(date.getUTCFullYear());
    return { key: year, label: year, tooltip: year };
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

  function renderUsageChart(range, report, mode, grouping) {
    const host = telemetryRoot.querySelector('[data-usage-chart="tokens"]');
    host.textContent = "";
    const points = pointsForRange(report, range);
    const spanDays = Math.max(1, Math.round((range.end - range.start) / 86400000) + 1);
    const buckets = new Map();
    for (let date = range.start; date <= range.end; date = addDays(date, 1)) {
      const bucket = bucketDetails(date, spanDays);
      if (!buckets.has(bucket.key)) buckets.set(bucket.key, { ...bucket, series: new Map(), cached: new Map() });
    }
    const seriesTotals = new Map();
    points.forEach((point) => {
      const key = seriesKey(point, grouping);
      const bucket = bucketDetails(utcDate(point.date), spanDays);
      const value = mode === "requests" ? point.requests : point.input_tokens + point.output_tokens;
      const target = buckets.get(bucket.key);
      if (!target) return;
      target.series.set(key, (target.series.get(key) || 0) + value);
      if (mode === "tokens") target.cached.set(key, (target.cached.get(key) || 0) + point.cached_input_tokens);
      seriesTotals.set(key, (seriesTotals.get(key) || 0) + value);
    });
    const orderedSeries = Array.from(seriesTotals.keys()).sort((left, right) => {
      const valueDifference = (seriesTotals.get(right) || 0) - (seriesTotals.get(left) || 0);
      if (valueDifference !== 0) return valueDifference;
      return seriesLabel(left, grouping).localeCompare(seriesLabel(right, grouping));
    });
    const bucketList = Array.from(buckets.values());
    const maximum = niceMaximum(Math.max(0, ...bucketList.map((bucket) => Array.from(bucket.series.values()).reduce((sum, value) => sum + value, 0))));
    if (orderedSeries.length === 0 || maximum === 0) {
      const empty = document.createElement("div");
      empty.className = "telemetry-empty";
      empty.textContent = orderedSeries.length === 0 ? "No usage in this period." : `No ${mode} were reported in this period.`;
      host.appendChild(empty);
      return;
    }

    const width = 1200;
    const height = 330;
    const left = 78;
    const right = 20;
    const top = 18;
    const bottom = 58;
    const plotWidth = width - left - right;
    const plotHeight = height - top - bottom;
    const svg = svgElement("svg", { viewBox: `0 0 ${width} ${height}`, role: "img", "aria-label": `Stacked ${grouping} ${mode} usage chart` });
    for (let tick = 0; tick <= 4; tick += 1) {
      const value = (maximum / 4) * tick;
      const y = top + plotHeight - (plotHeight * tick) / 4;
      svg.appendChild(svgElement("line", { x1: left, x2: width - right, y1: y, y2: y, class: "grid-line" }));
      const label = svgElement("text", { x: left - 10, y: y + 4, "text-anchor": "end" });
      label.textContent = formatCount(value);
      svg.appendChild(label);
    }
    svg.appendChild(svgElement("line", { x1: left, x2: width - right, y1: top + plotHeight, y2: top + plotHeight, class: "axis-line" }));
    const slot = plotWidth / Math.max(bucketList.length, 1);
    const barWidth = Math.max(3, Math.min(30, slot * 0.7));
    const labelEvery = Math.max(1, Math.ceil(bucketList.length / 12));
    bucketList.forEach((bucket, index) => {
      const x = left + index * slot + (slot - barWidth) / 2;
      let stacked = 0;
      orderedSeries.forEach((key) => {
        const value = bucket.series.get(key) || 0;
        if (value <= 0) return;
        const segmentHeight = (value / maximum) * plotHeight;
        const y = top + plotHeight - stacked - segmentHeight;
        svg.appendChild(svgElement("rect", { x, y, width: barWidth, height: Math.max(segmentHeight, 0.6), fill: colorFor(key) }));
        stacked += segmentHeight;
      });
      const values = orderedSeries.filter((key) => bucket.series.has(key)).map((key) => {
        const value = bucket.series.get(key) || 0;
        const cached = bucket.cached.get(key) || 0;
        return {
          color: colorFor(key),
          label: seriesLabel(key, grouping),
          numeric: value,
          cached,
        };
      }).sort((a, b) => b.numeric - a.numeric);
      const total = values.reduce((sum, row) => sum + row.numeric, 0);
      const cachedTotal = values.reduce((sum, row) => sum + row.cached, 0);
      const hit = svgElement("rect", { x: left + index * slot, y: top, width: Math.max(slot, 3), height: plotHeight, class: "bar-hit", tabindex: values.length ? 0 : -1, "aria-label": `${bucket.tooltip}, ${formatNumber(total)} ${mode}` });
      if (values.length) {
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
        const totalLabel = mode === "tokens"
          ? `${formatNumber(total)} tokens${cachedTotal > 0 ? `\n${formatNumber(cachedTotal)} cached` : ""}`
          : `${formatNumber(total)} requests`;
        hit.addEventListener("pointerenter", (event) => showTooltip(bucket.tooltip, tooltipRows, totalLabel, event.clientX, event.clientY));
        hit.addEventListener("pointermove", (event) => positionTooltip(event.clientX, event.clientY));
        hit.addEventListener("pointerleave", hideTooltip);
        hit.addEventListener("focus", () => showAnchoredTooltip(hit, bucket.tooltip, tooltipRows, totalLabel));
        hit.addEventListener("blur", hideTooltip);
      }
      svg.appendChild(hit);
      if (index % labelEvery === 0 || index === bucketList.length - 1) {
        const label = svgElement("text", { x: x + barWidth / 2, y: height - 25, "text-anchor": "middle" });
        label.textContent = bucket.label;
        svg.appendChild(label);
      }
    });
    host.appendChild(svg);
  }

  function earliestUsageDay(report) {
    if (!report.usage.length) return generatedDay(report);
    return utcDate(report.usage.reduce((earliest, point) => point.date < earliest ? point.date : earliest, report.usage[0].date));
  }

  function selectedRange(report, selection = telemetryRoot.querySelector("[data-telemetry-range]").value) {
    const today = generatedDay(report);
    if (selection === "today") return { start: today, end: today };
    if (selection === "week") return { start: addDays(today, -6), end: today };
    if (selection === "thirty") return { start: addDays(today, -29), end: today };
    if (selection === "month") return { start: new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1)), end: today };
    if (selection === "year") return { start: new Date(Date.UTC(today.getUTCFullYear(), 0, 1)), end: today };
    if (selection === "all") return { start: earliestUsageDay(report), end: today };
    const startValue = telemetryRoot.querySelector("[data-range-start]").value;
    const endValue = telemetryRoot.querySelector("[data-range-end]").value;
    if (!startValue || !endValue) return null;
    const earliest = earliestUsageDay(report);
    const requestedStart = utcDate(startValue);
    const requestedEnd = utcDate(endValue);
    const start = requestedStart < earliest ? earliest : requestedStart;
    const end = requestedEnd > today ? today : requestedEnd;
    if (start > end) return null;
    return { start, end };
  }

  function telemetryFailed() {
    telemetryRoot.querySelectorAll("[data-heatmap], [data-usage-chart]").forEach((host) => {
      host.innerHTML = '<div class="telemetry-empty">Telemetry could not be loaded. Reload after confirming the router session is active.</div>';
    });
    const breakdown = telemetryRoot.querySelector("[data-model-breakdown]");
    if (breakdown) breakdown.innerHTML = '<li class="breakdown-empty">Telemetry could not be loaded.</li>';
  }

  fetch("/admin/telemetry", { credentials: "same-origin", headers: { Accept: "application/json" } })
    .then((response) => {
      if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) throw new Error("telemetry unavailable");
      return response.json();
    })
    .then((report) => {
      const select = telemetryRoot.querySelector("[data-telemetry-range]");
      const custom = telemetryRoot.querySelector("[data-custom-range]");
      const startInput = telemetryRoot.querySelector("[data-range-start]");
      const endInput = telemetryRoot.querySelector("[data-range-end]");
      const rangeError = telemetryRoot.querySelector("[data-range-error]");
      const exportButton = telemetryRoot.querySelector("[data-export-telemetry]");
      const presets = Array.from(telemetryRoot.querySelectorAll("[data-range-preset]"));
      const customButton = telemetryRoot.querySelector('[data-range-preset="custom"]');
      let currentPoints = [];
      let currentRange = null;
      startInput.value = dateKey(earliestUsageDay(report));
      endInput.value = dateKey(generatedDay(report));
      startInput.min = dateKey(earliestUsageDay(report));
      startInput.max = dateKey(generatedDay(report));
      endInput.min = dateKey(earliestUsageDay(report));
      endInput.max = dateKey(generatedDay(report));
      renderHeatmap(report);

      const syncPresetButtons = (active = select.value) => {
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
      const render = (selection = select.value) => {
        const range = selectedRange(report, selection);
        if (!range) {
          rangeError.textContent = "Choose a valid start and end date.";
          rangeError.hidden = false;
          return false;
        }
        rangeError.hidden = true;
        select.value = selection;
        syncPresetButtons();
        const points = pointsForRange(report, range);
        currentPoints = points;
        currentRange = range;
        const mode = telemetryRoot.querySelector("[data-metric-mode]")?.value || "tokens";
        const grouping = telemetryRoot.querySelector("[data-group-mode]")?.value || "model";
        prepareSeriesColors(report.usage, grouping);
        setMetrics(points);
        renderUsageChart(range, report, mode, grouping);
        renderBreakdown(points, mode, grouping);
        updateChartMeta(range, mode, grouping);
        exportButton.disabled = false;
        return true;
      };
      select.addEventListener("change", () => {
        if (select.value === "custom") openCustomRange();
        else {
          closeCustomRange();
          render();
        }
      });
      presets.forEach((button) => button.addEventListener("click", () => {
        const selection = button.dataset.rangePreset;
        if (selection === "custom") {
          openCustomRange();
          return;
        }
        closeCustomRange();
        render(selection);
      }));
      telemetryRoot.querySelector("[data-metric-mode]")?.addEventListener("change", () => render());
      telemetryRoot.querySelector("[data-group-mode]")?.addEventListener("change", () => render());
      exportButton.addEventListener("click", () => {
        if (currentRange) exportTelemetry(currentPoints, currentRange);
      });
      telemetryRoot.querySelector("[data-apply-range]").addEventListener("click", () => {
        if (render("custom")) closeCustomRange();
      });
      telemetryRoot.querySelectorAll("[data-close-custom-range]").forEach((button) => {
        button.addEventListener("click", () => closeCustomRange(true));
      });
      [startInput, endInput].forEach((input) => input.addEventListener("keydown", (event) => {
        if (event.key === "Enter" && render("custom")) closeCustomRange();
      }));
      document.addEventListener("pointerdown", (event) => {
        if (!custom.hidden && !custom.contains(event.target) && !customButton.contains(event.target)) closeCustomRange();
      });
      document.addEventListener("keydown", (event) => {
        if (event.key === "Escape" && !custom.hidden) closeCustomRange(true);
      });
      render();
    })
    .catch(telemetryFailed);
})();
