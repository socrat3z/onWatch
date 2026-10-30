package web

import (
	"fmt"
	"testing"
)

// The Home health clock must age cached timestamps in place: during an outage no
// provider fetch succeeds, so nothing re-renders the cards, yet the freshness
// chips and banner have to keep telling the truth. Re-rendering the container
// would satisfy that too, but it drops keyboard focus, hover state and scroll
// position once a minute, so the behaviour under test is specifically that the
// live nodes survive.
//
// The script runs unmodified in a vm context against a minimal fake DOM. Slicing
// functions out of the source at comment markers would break on any rename and
// would not exercise the real globals the browser uses.
func TestHomepageHealthAgesInPlaceWithoutRerendering(t *testing.T) {
	t.Parallel()

	overlay, err := staticFS.ReadFile("static/fork_overlay.js")
	if err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf(`
const assert = require('node:assert/strict');
const vm = require('node:vm');

function makeElement(tagName) {
  const el = {
    tagName, className: '', dataset: {}, attrs: {}, children: [], ownText: '', style: {}, parent: null,
  };
  Object.defineProperty(el, 'textContent', {
    get() { return el.ownText + el.children.map(child => child.textContent).join(''); },
    set(value) { el.ownText = String(value); },
  });
  el.setAttribute = (key, value) => { el.attrs[key] = String(value); };
  el.getAttribute = key => (key in el.attrs ? el.attrs[key] : null);
  el.appendChild = child => { child.parent = el; el.children.push(child); return child; };
  el.prepend = child => { child.parent = el; el.children.unshift(child); return child; };
  el.remove = () => {
    if (el.parent) el.parent.children = el.parent.children.filter(candidate => candidate !== el);
    el.parent = null;
    el.removed = true;
  };
  el.replaceWith = node => {
    if (el.parent) {
      const index = el.parent.children.indexOf(el);
      el.parent.children[index] = node;
      node.parent = el.parent;
    }
    el.parent = null;
    el.removed = true;
    el.replacedBy = node;
  };
  el.closest = () => null;
  return el;
}

const snapshotAt = new Date(Date.now() - 29 * 60 * 1000).toISOString();
const resetAt = new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString();

const chip = makeElement('span');
chip.className = 'homepage-harness-freshness';
chip.dataset.snapshotAt = snapshotAt;
chip.dataset.freshness = 'fresh';
chip.textContent = '29m ago';

const title = makeElement('span');
title.textContent = 'Mistral';
const card = makeElement('section');
card.className = 'homepage-harness-card';
card.dataset.provider = 'mistral';
card.dataset.freshness = 'fresh';
card.querySelector = selector => {
  if (selector === '.provider-card-title span') return title;
  if (selector === '.homepage-harness-header-meta .homepage-harness-freshness') return chip;
  return null;
};
card.querySelectorAll = () => [];

const resetChip = makeElement('span');
resetChip.className = 'homepage-harness-reset';
resetChip.dataset.resetAt = resetAt;

const container = makeElement('main');
container.children = [card];
container.querySelectorAll = selector => (selector === '.homepage-harness-card' ? [card] : []);
container.querySelector = selector => {
  if (selector !== '.freshness-banner') return null;
  return container.children.find(child => child.className === 'freshness-banner') || null;
};

const document = {
  getElementById: id => (id === 'all-providers-container' ? container : null),
  querySelectorAll: selector => (selector === '.homepage-harness-reset[data-reset-at]' ? [resetChip] : []),
  createElement: makeElement,
};

const context = {
  document, console, setInterval, clearInterval, setTimeout, clearTimeout,
  State: {},
  parseDateValue: value => new Date(value),
  formatClockTime: value => new Date(value).toISOString(),
  escapeHTML: value => String(value),
};
context.globalThis = context;
vm.createContext(context);
new vm.Script(%s, { filename: 'fork_overlay.js' }).runInContext(context);

let now = Date.parse(snapshotAt) + 30 * 1000;
vm.runInContext('Date.now = () => ' + now, context);
context.updateHomepageHealth();
assert.equal(container.querySelector('.freshness-banner'), null, 'a fresh card must not raise a banner');
assert.equal(chip.dataset.freshness, 'fresh');

// One minute later the provider has not answered: the stored snapshot is still
// the newest data, so the chip has to age and the banner has to appear.
now = Date.parse(snapshotAt) + 31 * 60 * 1000;
vm.runInContext('Date.now = () => ' + now, context);
context.tickHomepageHealthAndResets();
assert.equal(card.querySelector('.homepage-harness-header-meta .homepage-harness-freshness'), chip,
  'the live chip node must be reused, not replaced');
assert.equal(chip.dataset.freshness, 'stale');
assert.equal(chip.textContent, '31m ago');
assert.equal(card.dataset.freshness, 'stale');

let banner = container.querySelector('.freshness-banner');
assert.ok(banner, 'stale data must raise a banner');
assert.match(banner.textContent, /1 provider with stale data/);
assert.match(banner.textContent, /Mistral/);
assert.equal(banner.dataset.freshness, 'stale');
assert.notEqual(resetChip.textContent, '', 'reset chips must refresh in the same pass');

// Past three hours the level escalates and the banner is swapped for the new
// copy while the cards keep their nodes.
now = Date.parse(snapshotAt) + 4 * 60 * 60 * 1000;
vm.runInContext('Date.now = () => ' + now, context);
context.tickHomepageHealthAndResets();
assert.equal(card.querySelector('.homepage-harness-header-meta .homepage-harness-freshness'), chip);
assert.equal(chip.dataset.freshness, 'error');
banner = container.querySelector('.freshness-banner');
assert.equal(banner.dataset.freshness, 'error');
assert.match(banner.textContent, /1 provider not updating/);

// A new snapshot arrives: the chip is fresh again and the banner is removed.
chip.dataset.snapshotAt = new Date(now).toISOString();
context.tickHomepageHealthAndResets();
assert.equal(chip.dataset.freshness, 'fresh');
assert.equal(container.querySelector('.freshness-banner'), null, 'a recovered provider must clear the banner');
`, jsStringLiteral(t, string(overlay)))
	runNodeScript(t, script)
}

// A card that groups accounts must age each account chip from its own stored
// time. A chip rendered without that time would read as "No data" on the first
// tick and flip every multi-account card to "not updating".
func TestHomepageHealthAgesEveryAccountChip(t *testing.T) {
	t.Parallel()

	overlay, err := staticFS.ReadFile("static/fork_overlay.js")
	if err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf(`
const assert = require('node:assert/strict');
const vm = require('node:vm');

function makeElement(tagName) {
  const el = { tagName, className: '', dataset: {}, attrs: {}, children: [], textContent: '', parent: null };
  el.setAttribute = (key, value) => { el.attrs[key] = String(value); };
  el.appendChild = child => { child.parent = el; el.children.push(child); return child; };
  el.prepend = child => { child.parent = el; el.children.unshift(child); return child; };
  el.closest = () => null;
  return el;
}

const context = {
  console, setInterval, clearInterval, setTimeout, clearTimeout,
  State: {},
  parseDateValue: value => (value ? new Date(value) : null),
  formatClockTime: value => new Date(value).toISOString(),
  escapeHTML: value => String(value),
  // app.js renderers; quota rows are outside this test.
  normalizeBothQuotas: () => [],
  renderHomepageMetricsHTML: () => '',
};
context.globalThis = context;
vm.createContext(context);
context.document = { getElementById: () => null, querySelectorAll: () => [], createElement: makeElement };
new vm.Script(%s, { filename: 'fork_overlay.js' }).runInContext(context);

const work = new Date(Date.now() - 5 * 60 * 1000).toISOString();
const personal = new Date(Date.now() - 10 * 60 * 1000).toISOString();
const html = context.renderHomepageAccountsHTML('antigravity', [
  { accountId: 1, accountName: 'work', snapshotAt: work, quotas: [] },
  { accountId: 2, accountName: 'personal', snapshotAt: personal, quotas: [] },
]);
const rendered = [...html.matchAll(/data-snapshot-at="([^"]*)"/g)].map(match => match[1]);
assert.deepEqual(rendered, [work, personal], 'account chips must carry their stored time');

const accounts = rendered.map(() => makeElement('div'));
const chips = rendered.map((snapshotAt, index) => {
  const chip = makeElement('span');
  chip.dataset.snapshotAt = snapshotAt;
  chip.closest = () => accounts[index];
  return chip;
});
const card = makeElement('section');
card.querySelector = () => null;
card.querySelectorAll = selector => (selector === '.homepage-harness-account .homepage-harness-freshness' ? chips : []);
const container = makeElement('main');
container.querySelectorAll = selector => (selector === '.homepage-harness-card' ? [card] : []);
container.querySelector = () => null;
context.document.getElementById = id => (id === 'all-providers-container' ? container : null);

context.updateHomepageHealth();
assert.equal(card.dataset.freshness, 'fresh', 'two fresh accounts must not read as No data');
assert.deepEqual(accounts.map(account => account.dataset.freshness), ['fresh', 'fresh']);
assert.equal(container.children.length, 0, 'no banner for fresh accounts');

// Forty minutes on, both accounts have aged and the oldest decides the card.
vm.runInContext('Date.now = () => ' + (Date.parse(work) + 40 * 60 * 1000), context);
context.updateHomepageHealth();
assert.deepEqual(chips.map(chip => chip.textContent), ['40m ago', '45m ago']);
assert.equal(card.dataset.freshness, 'stale');
assert.equal(container.children[0].dataset.freshness, 'stale');
`, jsStringLiteral(t, string(overlay)))
	runNodeScript(t, script)
}
