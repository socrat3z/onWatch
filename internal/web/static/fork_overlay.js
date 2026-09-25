// onWatch Fork Overlay JavaScript Extensions
// Isolates fork-specific multi-account UI, data freshness indicators, and masonry layout.

// ── Shared account picker (Anthropic + Antigravity) ───────────────────────
function providerAccountParam(provider) {
  const id = State.providerAccount[provider];
  return id ? `&account=${encodeURIComponent(id)}` : '';
}

function providerAccountStorageKey(provider) { return `onwatch-${provider}-account`; }

// The legacy account holds everything polled before this install had named
// accounts. It is not a real credential directory, so it is labelled rather
// than silently sitting in the picker looking like one more login.
const LEGACY_ACCOUNT_BADGE = 'Before account split';
const LEGACY_ACCOUNT_NOTE = 'Holds the history recorded before named accounts were enabled. It keeps polling only if a credential folder named "default" exists.';

const accountHealthCopy = {
  missing: {
    label: 'No credentials',
    detail: (path) => path
      ? `onWatch found no credential file at ${path}. Log in for this account, then it starts polling within a minute.`
      : 'onWatch found no credential file for this account. Log in for this account, then it starts polling within a minute.',
  },
  unreadable: {
    label: 'Credentials unreadable',
    detail: (path) => path
      ? `The credential file at ${path} could not be parsed. Log in again to rewrite it.`
      : 'This account’s credential file could not be parsed. Log in again to rewrite it.',
  },
  unverified: {
    label: 'Unverified',
    detail: () => 'onWatch cannot check this login without reading the keyring. If no data appears, run the login for this account.',
  },
};

function accountHealthState(account) {
  return (account && account.health && account.health.credentials) || 'unverified';
}

// An account is only called out when there is something to act on. "ok" and
// Antigravity's permanent "unverified" are both silent so a healthy install
// never grows a warning it cannot clear.
function accountHealthProblem(account) {
  const state = accountHealthState(account);
  if (state === 'ok' || state === 'unverified') return null;
  return accountHealthCopy[state] || null;
}

function providerAccountLabel(account) {
  if (!account) return 'Account';
  return account.alias || account.name || 'Account';
}

// The card label is the account name only where it disambiguates - a
// single-account install must look exactly as it did before.
function providerAccountCardLabel(account) {
  if (!account || Number(account.accountCount || 0) <= 1) return '';
  return providerAccountLabel(account);
}

function renderProviderAccountNote(provider, account) {
  const note = document.getElementById('provider-account-note');
  if (!note) return;
  const messages = [];
  const problem = account ? accountHealthProblem(account) : null;
  if (problem) messages.push(`${problem.label}: ${problem.detail((account.health || {}).credentialPath || '')}`);
  if (account && account.isDefault) messages.push(`${LEGACY_ACCOUNT_BADGE}: ${LEGACY_ACCOUNT_NOTE}`);
  if (messages.length === 0) {
    note.hidden = true;
    note.textContent = '';
    return;
  }
  note.hidden = false;
  note.textContent = messages.join(' ');
}

async function loadProviderAccounts(provider) {
  try {
    const res = await authFetch(`${API_BASE}/api/accounts?provider=${encodeURIComponent(provider)}`);
    if (!res.ok) return;
    const data = await res.json();
    const accounts = (data.accounts || []).filter(a => !a.deletedAt);
    State.providerAccounts[provider] = accounts;
    const saved = parseInt(localStorage.getItem(providerAccountStorageKey(provider)), 10);
    State.providerAccount[provider] = accounts.some(a => a.id === saved) ? saved : (accounts[0] || {}).id || null;
    renderProviderAccountPicker();
  } catch (_) { /* A single-account install does not need a visible picker. */ }
}

function renderProviderAccountPicker() {
  const dropdown = document.getElementById('provider-account-dropdown');
  const menu = document.getElementById('provider-account-menu');
  const label = document.getElementById('provider-account-label');
  const provider = getCurrentProvider();
  if (!dropdown || !menu || !label) return;
  const accounts = State.providerAccounts[provider] || [];
  // One account still shows its name and stays renameable - the first account a
  // user names was previously invisible until a second one existed. Zero
  // accounts adds nothing.
  if ((provider !== 'anthropic' && provider !== 'antigravity') || accounts.length === 0) {
    dropdown.style.display = 'none';
    renderProviderAccountNote(provider, null);
    return;
  }
  dropdown.style.display = '';
  menu.innerHTML = '';
  for (const account of accounts) {
    const item = document.createElement('li');
    const selected = account.id === State.providerAccount[provider];
    const problem = accountHealthProblem(account);
    item.className = 'codex-profile-item' + (selected ? ' active' : '') + (problem ? ' provider-account-unhealthy' : '');
    item.textContent = providerAccountLabel(account);
    if (account.isDefault) {
      const legacy = document.createElement('span');
      legacy.className = 'provider-account-tag';
      legacy.textContent = LEGACY_ACCOUNT_BADGE;
      item.appendChild(legacy);
      item.title = LEGACY_ACCOUNT_NOTE;
    }
    if (problem) {
      const warn = document.createElement('span');
      warn.className = 'provider-account-tag provider-account-tag-warn';
      warn.textContent = problem.label;
      item.appendChild(warn);
      item.title = problem.detail((account.health || {}).credentialPath || '');
    }
    item.setAttribute('role', 'option');
    item.setAttribute('aria-selected', selected ? 'true' : 'false');
    item.addEventListener('click', () => {
      State.providerAccount[provider] = account.id;
      localStorage.setItem(providerAccountStorageKey(provider), account.id);
      renderProviderAccountPicker();
      closeProviderAccountPicker();
      refreshAll();
    });
    menu.appendChild(item);
  }
  const rename = document.createElement('li');
  rename.className = 'codex-profile-item provider-account-alias-action';
  rename.textContent = 'Edit display alias…';
  rename.setAttribute('role', 'option');
  rename.addEventListener('click', () => {
    const active = accounts.find(a => a.id === State.providerAccount[provider]);
    if (!active) return;
    closeProviderAccountPicker();
    openProviderAccountAliasDialog(provider, active);
  });
  menu.appendChild(rename);
  const active = accounts.find(a => a.id === State.providerAccount[provider]);
  label.textContent = providerAccountLabel(active);
  renderProviderAccountNote(provider, active);
}

// openProviderAccountAliasDialog replaces window.prompt/window.alert: the rule
// is stated before submission and the server's own message is shown verbatim,
// instead of a guess that contradicted it.
function openProviderAccountAliasDialog(provider, account) {
  const modal = document.getElementById('detail-modal');
  const titleEl = document.getElementById('modal-title');
  const bodyEl = document.getElementById('modal-body');
  if (!modal || !titleEl || !bodyEl) return;

  titleEl.textContent = 'Edit display alias';
  bodyEl.innerHTML = `
    <form class="account-alias-form" id="account-alias-form">
      <p class="insight-text">Renaming changes only the label shown in onWatch. The credential folder <code>${escapeHTML(account.name)}</code> is not touched, so polling keeps working.</p>
      <label class="account-alias-label" for="account-alias-input">Display alias</label>
      <input class="account-alias-input" id="account-alias-input" type="text" maxlength="64" value="${escapeHTML(account.alias || account.name)}" autocomplete="off">
      <p class="account-alias-hint">Use 1-64 characters.</p>
      <p class="account-alias-error" id="account-alias-error" role="alert" hidden></p>
      <div class="account-alias-actions">
        <button type="button" class="header-btn" id="account-alias-cancel">Cancel</button>
        <button type="submit" class="header-btn" id="account-alias-save">Save</button>
      </div>
    </form>`;
  modal.hidden = false;

  const form = document.getElementById('account-alias-form');
  const input = document.getElementById('account-alias-input');
  const error = document.getElementById('account-alias-error');
  if (input) { input.focus(); input.select(); }
  const cancel = document.getElementById('account-alias-cancel');
  if (cancel) cancel.addEventListener('click', closeModal);

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const alias = (input.value || '').trim();
    error.hidden = true;
    try {
      const res = await authFetch(`${API_BASE}/api/accounts?provider=${encodeURIComponent(provider)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ account_id: account.id, alias }),
      });
      if (!res.ok) {
        // The server already explains precisely what it rejected; repeat it.
        let message = 'Could not save that display alias.';
        try { const payload = await res.json(); if (payload && payload.error) message = payload.error; } catch (_) { /* keep fallback */ }
        error.textContent = message;
        error.hidden = false;
        return;
      }
      closeModal();
      await loadProviderAccounts(provider);
    } catch (err) {
      error.textContent = 'Could not reach onWatch to save that alias.';
      error.hidden = false;
    }
  });
}

function closeProviderAccountPicker() {
  const trigger = document.getElementById('provider-account-trigger');
  const menu = document.getElementById('provider-account-menu');
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  if (menu) menu.classList.remove('open');
}

function initProviderAccountPicker() {
  const trigger = document.getElementById('provider-account-trigger');
  const menu = document.getElementById('provider-account-menu');
  if (!trigger || !menu) return;
  trigger.addEventListener('click', e => { e.stopPropagation(); const open = menu.classList.toggle('open'); trigger.setAttribute('aria-expanded', open ? 'true' : 'false'); });
  document.addEventListener('click', e => { if (!e.target.closest('#provider-account-dropdown')) closeProviderAccountPicker(); });
  document.addEventListener('keydown', e => { if (e.key === 'Escape') closeProviderAccountPicker(); });
}


// ── Data freshness ──
//
// A failed poll leaves the stored snapshot untouched, so snapshot age is the
// ground truth for both "slipping behind" and "broken": the age of the newest
// stored snapshot is how old the numbers on screen actually are.
const FRESHNESS_STALE_MS = 30 * 60 * 1000;
const FRESHNESS_ERROR_MS = 3 * 60 * 60 * 1000;

function formatFreshnessAge(ms) {
  if (!Number.isFinite(ms)) return '';
  if (ms < 60000) return 'just now';
  const mins = Math.floor(ms / 60000);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

// providerFreshness maps a snapshot timestamp to a display state.
// 'error' also covers "never polled successfully" (no snapshot at all).
function providerFreshness(snapshotAt) {
  const d = parseDateValue(snapshotAt);
  if (!d) {
    return {
      state: 'error',
      label: 'No data',
      title: 'No snapshot stored yet - polling has never succeeded for this provider.',
      ageMs: Infinity,
    };
  }
  const ageMs = Date.now() - d.getTime();
  const stamp = `Last update ${formatClockTime(d)}`;
  const label = formatFreshnessAge(ageMs);
  if (ageMs >= FRESHNESS_ERROR_MS) {
    return { state: 'error', label, title: `Data is over 3h old - polling is likely failing. ${stamp}`, ageMs };
  }
  if (ageMs >= FRESHNESS_STALE_MS) {
    return { state: 'stale', label, title: `Data is over 30m old. ${stamp}`, ageMs };
  }
  return { state: 'fresh', label, title: stamp, ageMs };
}

// Cards that group several accounts report their worst (oldest) member, so one
// silently dead account cannot hide behind a healthy sibling.
function oldestSnapshotAt(payloads) {
  let oldest = null;
  let sawMissing = false;
  (Array.isArray(payloads) ? payloads : []).forEach((payload) => {
    const d = parseDateValue(payload?.snapshotAt);
    if (!d) { sawMissing = true; return; }
    if (!oldest || d.getTime() < oldest.getTime()) oldest = d;
  });
  if (sawMissing) return null;
  return oldest ? oldest.toISOString() : null;
}

function freshnessChipHTML(freshness) {
  if (!freshness || freshness.state === 'unknown') return '';
  return `<span class="homepage-harness-freshness" data-freshness="${escapeHTML(freshness.state)}" title="${escapeHTML(freshness.title)}">${escapeHTML(freshness.label)}</span>`;
}

// Banner summarising every card that is not fresh, so the problem is visible
// without scanning each card.
function renderFreshnessBannerHTML(entries) {
  const degraded = (Array.isArray(entries) ? entries : [])
    .filter(entry => entry.freshness && (entry.freshness.state === 'stale' || entry.freshness.state === 'error'));
  if (degraded.length === 0) return '';

  const failing = degraded.filter(entry => entry.freshness.state === 'error');
  const slipping = degraded.length - failing.length;
  const level = failing.length > 0 ? 'error' : 'stale';
  const parts = [];
  if (failing.length > 0) {
    parts.push(`${failing.length} provider${failing.length === 1 ? '' : 's'} not updating`);
  }
  if (slipping > 0) {
    parts.push(failing.length > 0
      ? `${slipping} with stale data`
      : `${slipping} provider${slipping === 1 ? '' : 's'} with stale data`);
  }
  const headline = parts.join(', ');
  const detail = degraded
    .map(entry => `${entry.title} (${entry.freshness.label})`)
    .join(', ');

  return `<div class="freshness-banner" data-freshness="${level}" role="status">
    <span class="freshness-banner-dot" aria-hidden="true"></span>
    <div class="freshness-banner-text">
      <strong>${escapeHTML(headline)}</strong>
      <span>${escapeHTML(detail)}</span>
    </div>
  </div>`;
}

// Multi-account providers ship one payload per profile under these keys once a
// second account exists, and the flat provider key otherwise. Codex and MiniMax
// still need their own single-account branches below, because only they group
// insights and history per account as well.
const MULTI_ACCOUNT_PROVIDER_KEYS = {
  anthropic: 'anthropicAccounts',
  antigravity: 'antigravityAccounts',
  codex: 'codexAccounts',
  minimax: 'minimaxAccounts',
};

const MULTI_ACCOUNT_PROVIDER_FROM_KEY = Object.fromEntries(
  Object.entries(MULTI_ACCOUNT_PROVIDER_KEYS).map(([provider, key]) => [key, provider]));

// One card per provider, one widget per profile inside it - the grouping rule
// for every multi-account provider, so a fifth one needs no new branch. An
// account switched off in settings is dropped here, matching the backend.
function groupedAccountEntry(provider, accounts) {
  const visible = (Array.isArray(accounts) ? accounts : []).filter((account, idx) =>
    isProviderTelemetryEnabled(provider, account.accountId || account.id || idx + 1));
  if (visible.length === 0) return null;
  return {
    provider,
    cardKey: sanitizeProviderCardKey(provider),
    title: bothProviderNames[provider] || toTitleCase(provider),
    badge: `${visible.length} accounts`,
    // The promo is provider-wide, so any account can carry it.
    promoHtml: provider === 'anthropic' && visible.some(account => account && account.promo)
      ? promoTagHTML()
      : '',
    snapshotAt: oldestSnapshotAt(visible),
    accountsGroup: visible,
  };
}


function homepageResetSeconds(quota) {
  if (quota && quota.resetsAt) {
    const at = new Date(quota.resetsAt).getTime();
    if (Number.isFinite(at)) return Math.max(0, Math.round((at - Date.now()) / 1000));
  }
  const secs = Number(quota && quota.timeUntilResetSeconds);
  return Number.isFinite(secs) && secs > 0 ? Math.round(secs) : 0;
}

// Two units at most, no spaces: 5h12m, 2d3h, 47m. The homepage has room for a
// glance, not a duration breakdown - the provider page keeps the long form.
function formatHomepageReset(seconds) {
  if (!(seconds > 0)) return '';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return h > 0 ? `${d}d${h}h` : `${d}d`;
  if (h > 0) return m > 0 ? `${h}h${m}m` : `${h}h`;
  if (m > 0) return `${m}m`;
  return '<1m';
}

// Quotas that carry no reset time (balances, credit pools) render without a
// chip rather than with a placeholder.
function homepageResetChipHTML(quota) {
  const seconds = homepageResetSeconds(quota);
  const text = formatHomepageReset(seconds);
  if (!text) return '';
  const attr = quota.resetsAt ? ` data-reset-at="${escapeHTML(String(quota.resetsAt))}"` : '';
  return `<span class="homepage-harness-reset"${attr} title="Resets in ${escapeHTML(text)}">${escapeHTML(text)}</span>`;
}

// Recomputes the chips in place so a card that sits on screen between polls
// still counts down. Cheap enough at a minute's cadence: it only touches text.
function startHomepageResetTicks() {
  if (State.homepageResetInterval) return;
  State.homepageResetInterval = setInterval(() => {
    const chips = document.querySelectorAll('.homepage-harness-reset[data-reset-at]');
    if (chips.length === 0) return;
    chips.forEach((chip) => {
      const text = formatHomepageReset(homepageResetSeconds({ resetsAt: chip.dataset.resetAt }));
      // Past the reset the poller has not confirmed the new window yet, so say
      // "due" instead of counting into negatives.
      chip.textContent = text || 'due';
      chip.title = text ? `Resets in ${text}` : 'Reset due';
    });
  }, 60000);
}


function renderHomepageAccountsHTML(provider, accounts) {
  return (Array.isArray(accounts) ? accounts : []).map((account, idx) => {
    const accountId = account.accountId || account.id || idx + 1;
    const accountName = account.accountName || account.name || `Account ${accountId}`;
    const quotas = normalizeBothQuotas(provider, account);
    const freshness = providerFreshness(account.snapshotAt);
    return `<div class="homepage-harness-account" data-account-id="${escapeHTML(accountId)}" data-freshness="${escapeHTML(freshness.state)}" role="button" tabindex="0" aria-label="Open ${escapeHTML(accountName)}">
      <div class="homepage-harness-account-name">
        <span>${escapeHTML(accountName)}</span>
        ${freshnessChipHTML(freshness)}
      </div>
      ${renderHomepageMetricsHTML(quotas)}
    </div>`;
  }).join('');
}


const HOMEPAGE_MASONRY_ROW = 4;

// Packs the harness cards so a short card stops reserving the tallest card's
// height, without touching their sequence: each card keeps its grid position
// and only its row span changes. Bails out on a single-column layout, where
// there is nothing to pack.
function layoutHomepageMasonry(container) {
  if (!container) return;
  const cards = Array.from(container.querySelectorAll('.homepage-harness-card'));
  if (cards.length === 0) {
    container.classList.remove('is-masonry');
    return;
  }

  // The freshness banner is a grid item too, and once is-masonry sets
  // grid-auto-rows to HOMEPAGE_MASONRY_ROW every implicit row is that tall.
  // Left unspanned the banner gets one 4px row, overflows it, and the cards
  // placed on the following rows paint over its text. It is measured with the
  // cards rather than special-cased so it stays correct when its detail line
  // wraps to a second line.
  const banners = Array.from(container.querySelectorAll('.freshness-banner'));
  const items = banners.concat(cards);

  const styles = window.getComputedStyle(container);
  const columns = styles.getPropertyValue('grid-template-columns').trim().split(/\s+/).filter(Boolean).length;
  if (columns < 2) {
    container.classList.remove('is-masonry');
    items.forEach((item) => { item.style.gridRowEnd = ''; });
    return;
  }

  const gap = parseFloat(styles.getPropertyValue('row-gap')) || 0;
  // Measure every item before writing any span back, so one item's new span
  // cannot reflow the next item mid-measurement.
  const heights = items.map((item) => {
    item.style.gridRowEnd = '';
    return item.getBoundingClientRect().height;
  });
  // A hidden container measures every card at zero; spanning one row each would
  // stack them all on top of each other once it is shown. Leave the plain grid
  // in place - the resize observer re-runs this when the cards get a size.
  if (!heights.some(height => height > 0)) {
    container.classList.remove('is-masonry');
    return;
  }
  items.forEach((item, i) => {
    const span = Math.max(1, Math.ceil((heights[i] + gap) / (HOMEPAGE_MASONRY_ROW + gap)));
    item.style.gridRowEnd = `span ${span}`;
  });
  container.style.setProperty('--homepage-masonry-row', `${HOMEPAGE_MASONRY_ROW}px`);
  container.classList.add('is-masonry');
}

// Card heights change on resize (labels wrap) and when a countdown chip grows,
// so re-pack on both. One observer per container, replaced on re-render.
function observeHomepageMasonry(container) {
  if (!container || typeof ResizeObserver === 'undefined') return;
  if (State.homepageMasonryObserver) State.homepageMasonryObserver.disconnect();
  let queued = false;
  const observer = new ResizeObserver(() => {
    if (queued) return;
    queued = true;
    window.requestAnimationFrame(() => {
      queued = false;
      layoutHomepageMasonry(container);
    });
  });
  observer.observe(container);
  // The banner is observed alongside the cards: its detail line wraps at narrow
  // widths, which changes the row span it needs.
  container.querySelectorAll('.freshness-banner, .homepage-harness-card').forEach((item) => observer.observe(item));
  State.homepageMasonryObserver = observer;
}

