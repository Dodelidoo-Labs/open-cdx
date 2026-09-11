(() => {
  const dialog = document.querySelector("[data-reset-dialog]");
  if (!dialog) return;
  const apply = dialog.querySelector("[data-reset-apply]");
  const cancel = dialog.querySelector("[data-reset-cancel]");
  const result = dialog.querySelector("[data-reset-result]");
  let selection = null;
  let busy = false;
  const pendingKeys = new Map();
  const icon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 5h18v5a2 2 0 0 0 0 4v5H3v-5a2 2 0 0 0 0-4V5Z"/><path d="M15 7v2m0 2v2m0 2v2"/></svg>';
  const expired = value => value && Date.parse(value) <= Date.now();

  function expireTickets() {
    document.querySelectorAll("[data-reset-expires]").forEach(button => {
      if (expired(button.dataset.resetExpires)) button.remove();
    });
    if (dialog.open && selection && expired(selection.expiresAt) && !busy) {
      apply.disabled = true;
      result.textContent = "This reset has expired.";
    }
  }

  document.addEventListener("opencdx:reset-tickets", ({ detail: { row, account } }) => {
    const host = row.querySelector("[data-reset-tickets]");
    if (!host) return;
    host.replaceChildren(...(account.reset_tickets || []).filter(ticket => !expired(ticket.expires_at)).map(ticket => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "reset-ticket";
      button.dataset.resetCredit = ticket.id || "";
      button.dataset.resetExpires = ticket.expires_at || "";
      button.setAttribute("aria-label", `Apply one banked reset for ${account.masked_email}`);
      button.setAttribute("aria-haspopup", "dialog");
      button.title = ticket.expires_at ? `Apply one reset · expires ${new Date(ticket.expires_at).toLocaleString()}` : "Apply one banked reset";
      button.innerHTML = icon;
      button.disabled = busy;
      return button;
    }));
  });

  document.addEventListener("click", event => {
    const button = event.target.closest("[data-reset-credit]");
    if (!button || busy || expired(button.dataset.resetExpires)) return;
    const row = button.closest("[data-account-id]");
    if (!row) return;
    selection = { accountID: row.dataset.accountId, creditID: button.dataset.resetCredit, expiresAt: button.dataset.resetExpires };
    dialog.querySelector("[data-reset-description]").textContent = `Use one banked reset for ${row.dataset.accountEmail}.`;
    result.textContent = "";
    apply.hidden = false;
    apply.textContent = "Apply Reset";
    apply.disabled = false;
    cancel.textContent = "Cancel";
    dialog.showModal();
    cancel.focus();
  });
  cancel.addEventListener("click", () => { if (!busy) dialog.close(); });
  dialog.addEventListener("cancel", event => { if (busy) event.preventDefault(); });
  dialog.addEventListener("close", () => { selection = null; });

  apply.addEventListener("click", async () => {
    if (busy || !selection || expired(selection.expiresAt)) return;
    const chosen = selection;
    const storageKey = `opencdx-reset:${chosen.accountID}:${chosen.creditID || "next"}`;
    let key = pendingKeys.get(storageKey);
    try { key ||= localStorage.getItem(storageKey); } catch {}
    key ||= crypto.randomUUID ? crypto.randomUUID() : Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, "0")).join("");
    pendingKeys.set(storageKey, key);
    try { localStorage.setItem(storageKey, key); } catch {}
    busy = true;
    apply.disabled = cancel.disabled = true;
    result.textContent = "Applying one reset…";
    try {
      const response = await fetch(`/admin/accounts/${encodeURIComponent(chosen.accountID)}/resets/consume`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('input[name="csrf"]').value },
        body: JSON.stringify({ idempotency_key: key, ...(chosen.creditID ? { credit_id: chosen.creditID } : {}) }),
        signal: AbortSignal.timeout(90000),
      });
      if (!response.ok) throw new Error("Reset request failed");
      const data = await response.json();
      const messages = {
        reset: "One reset applied.",
        alreadyRedeemed: "This reset was already applied.",
        nothingToReset: "No eligible allowance needs resetting. Your ticket was kept.",
        noCredit: "No reset remains available for this account.",
      };
      if (!Object.hasOwn(messages, data.outcome)) throw new Error("Unknown reset outcome");
      pendingKeys.delete(storageKey);
      try { localStorage.removeItem(storageKey); } catch {}
      result.textContent = messages[data.outcome] + (data.quotas_refreshed ? "" : " Usage refresh is pending.");
      apply.hidden = true;
      cancel.textContent = "Close";
      document.dispatchEvent(new Event("opencdx:reset-completed"));
    } catch {
      result.textContent = "Could not confirm the reset. Retry safely with the same ticket.";
      apply.textContent = "Retry";
    } finally {
      busy = false;
      apply.disabled = cancel.disabled = false;
      expireTickets();
    }
  });
  expireTickets();
  window.setInterval(expireTickets, 1000);
})();
