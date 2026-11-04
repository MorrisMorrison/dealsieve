import {
  applySuggestion, deleteAction, dismissSuggestion, getActions, getCategories, getHistory, getSettings, getSuggestions,
  putSettings, testChannel, testDigest, testJudge, undoSettings,
} from './api.js';
import { ago, clickKind, sentence } from './format.js';
import { initTheme } from './theme.js';
import { addTo, changeCount, clone, nextChannelName, removeFrom, TIMEZONES, withMinTemperature } from './settings-model.js';

const $ = (sel, root = document) => root.querySelector(sel);

// Builds an element without innerHTML: props are assigned as properties, except
// `on` (listeners) and `attrs` (attributes). `value` goes last so selects have options.
function h(tag, props = {}, ...kids) {
  const el = document.createElement(tag);
  let value;
  for (const [k, v] of Object.entries(props)) {
    if (k === 'on') for (const [ev, fn] of Object.entries(v)) el.addEventListener(ev, fn);
    else if (k === 'attrs') for (const [a, val] of Object.entries(v)) el.setAttribute(a, val);
    else if (k === 'value') value = v;
    else el[k] = v;
  }
  el.append(...kids.flat().filter((k) => k != null && k !== false));
  if (value !== undefined) el.value = value;
  return el;
}

const st = {
  saved: null, draft: null, version: 0, warnings: {}, judgeEnv: {},
  suggestions: [], history: [], categories: [], clicks: [],
  sites: [], errors: {}, addingChannel: false, stale: false,
};
let shown = new Set();

const sections = {
  suggestions: renderSuggestions, watchlist: renderWatchlist, sources: renderSources, filters: renderFilters,
  recurrence: renderRecurrence, notifications: renderNotifications, clicks: renderClicks, history: renderHistory,
};

function render(name) {
  $(`#${name} [data-body]`).replaceChildren(...sections[name]());
}

function renderAll() {
  shown = new Set();
  for (const name of Object.keys(sections)) render(name);
  updateBar();
}

// A change to the draft: refresh the save bar and re-render the section it touched.
function touch(section, focusSel) {
  updateBar();
  render(section);
  if (focusSel) $(focusSel)?.focus();
}

function updateBar() {
  const n = st.draft ? changeCount(st.saved, st.draft) : 0;
  $('#savebar').classList.toggle('show', n > 0);
  $('#dirty-count').textContent = String(n);
  $('#dirty-label').textContent = n === 1 ? 'unsaved change' : 'unsaved changes';
}

// ---- validation errors from a 422, shown under the field that owns them

function errorNodes(pred) {
  const nodes = [];
  for (const [path, msg] of Object.entries(st.errors)) {
    if (!pred(path)) continue;
    shown.add(path);
    const n = h('div', { className: 'field-error', textContent: msg });
    n._pred = pred;
    nodes.push(n);
  }
  return nodes;
}

function clearErrors(pred, ...inputs) {
  for (const path of Object.keys(st.errors)) if (pred(path)) delete st.errors[path];
  document.querySelectorAll('.field-error').forEach((n) => { if (n._pred === pred) n.remove(); });
  inputs.forEach((i) => i.classList.remove('invalid'));
}

const exact = (...paths) => (p) => paths.includes(p);
const under = (prefix) => (p) => p === prefix || p.startsWith(`${prefix}.`) || p.startsWith(`${prefix}[`);

// ---- small builders

function select(options, value, onChange, label) {
  return h('select', { value, on: { change: (e) => onChange(e.target.value) }, attrs: { 'aria-label': label } },
    options.map(([v, text, disabled]) => h('option', { value: v, textContent: text, disabled: !!disabled })));
}

function numberInput(value, onInput, { min, max, label, width, pred } = {}) {
  const input = h('input', {
    type: 'number', value: String(value),
    attrs: { 'aria-label': label || '', step: 1, ...(min !== undefined ? { min } : {}), ...(max !== undefined ? { max } : {}) },
    on: {
      input: (e) => {
        if (pred) clearErrors(pred, input);
        input.nextError?.remove();
        input.nextError = null;
        // An empty or non-integer field would become 0 or a decimal; keep the
        // last good value in the draft and block saving until it is fixed.
        const v = input.validity;
        if (e.target.value === '' || v.badInput || v.stepMismatch) {
          input.dataset.bad = '1';
          input.classList.add('invalid');
          input.nextError = h('div', { className: 'field-error', textContent: 'Enter a whole number' });
          (input.closest('.override, .field') || input.parentNode).append(input.nextError);
          return;
        }
        delete input.dataset.bad;
        input.classList.remove('invalid');
        onInput(Number(e.target.value));
        updateBar();
      },
    },
  });
  if (width) input.style.width = width;
  if (pred && Object.keys(st.errors).some(pred)) input.classList.add('invalid');
  return input;
}

function chipsField({ key, label, help, list, cls, placeholder, section, set, pred, datalistId, options }) {
  const input = h('input', {
    type: 'text', placeholder, attrs: { 'aria-label': label, ...(datalistId ? { list: datalistId } : {}) },
    on: {
      keydown: (e) => {
        if (e.key !== 'Enter') return;
        e.preventDefault();
        const next = addTo(list, input.value);
        if (next === list) { input.value = ''; return; }
        set(next);
        clearErrors(pred);
        touch(section, `[data-chips="${key}"] input`);
      },
    },
  });
  const chips = list.map((v) => h('span', { className: `chip ${cls}` }, v,
    h('button', {
      type: 'button', textContent: '×', attrs: { 'aria-label': `Remove ${v}` },
      on: { click: () => { set(removeFrom(list, v)); clearErrors(pred); touch(section, `[data-chips="${key}"] input`); } },
    })));
  const errs = errorNodes(pred);
  const box = h('div', { className: `chips${errs.length ? ' invalid' : ''}`, attrs: { 'data-chips': key } }, chips, input);
  return h('div', { className: 'field' },
    key !== 'watchlist' ? h('span', { className: 'label', textContent: label }) : null,
    box,
    datalistId ? h('datalist', { id: datalistId }, options.map((o) => h('option', { value: o }))) : null,
    help ? h('span', { className: 'help', textContent: help }) : null,
    errs);
}

// ---- sections

function renderSuggestions() {
  const badge = $('#suggestion-count');
  badge.textContent = String(st.suggestions.length);
  badge.hidden = st.suggestions.length === 0;
  $('#suggestions .lead').textContent =
    `Based on what you hid or marked as junk in the last ${st.saved.suggestions.window_days} days.`;

  const items = st.suggestions.map((s) => {
    const merchant = s.kind === 'mute_merchant';
    const since = new Date(s.since).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
    const apply = h('button', {
      type: 'button', className: 'btn primary small', textContent: merchant ? 'Mute shop' : 'Mute category',
      on: { click: () => onApply(s, apply, dismiss) },
    });
    const dismiss = h('button', {
      type: 'button', className: 'btn ghost small', textContent: 'Dismiss',
      on: { click: () => onDismiss(s, apply, dismiss) },
    });
    return h('div', { className: 'suggestion' },
      h('div', { className: 'grow' },
        h('strong', { textContent: `Mute ${merchant ? 'shop' : 'category'} “${s.value}”?` }),
        h('div', { className: 'help', textContent: `You hid or junked ${s.count} of its deals since ${since}.` }),
        s.examples?.length ? h('div', { className: 'help examples', textContent: `e.g. ${s.examples.slice(0, 3).join(' · ')}` }) : null),
      apply, dismiss);
  });
  const sg = st.draft.suggestions;
  const countPred = exact('suggestions.min_count');
  const windowPred = exact('suggestions.window_days');
  return [
    ...(items.length ? items : [h('p', { className: 'empty', textContent: 'No suggestions right now.' })]),
    h('div', { className: 'field', style: 'margin-top:14px' },
      h('div', { className: 'row help' }, 'Suggest after ',
        numberInput(sg.min_count, (v) => { sg.min_count = v; }, { min: 2, label: 'Hides or junks before suggesting', width: '64px', pred: countPred }),
        ' hides or junks within ',
        numberInput(sg.window_days, (v) => { sg.window_days = v; }, { min: 1, label: 'Suggestion window in days', width: '72px', pred: windowPred }),
        ' days.'),
      errorNodes(countPred), errorNodes(windowPred)),
  ];
}

function renderWatchlist() {
  return [chipsField({
    key: 'watchlist', label: 'Add a watchlist keyword', list: st.draft.watchlist, cls: 'watch',
    placeholder: 'Add a keyword and press Enter', section: 'watchlist',
    set: (v) => { st.draft.watchlist = v; }, pred: under('watchlist'),
  })];
}

function renderSources() {
  const doc = st.draft.sources;
  const rows = st.sites.map((site) => {
    const entry = () => doc[site.name] ?? { enabled: false };
    const pred = under(`sources.${site.name}`);
    const input = h('input', {
      type: 'number', value: entry().min_temperature === undefined ? '' : String(entry().min_temperature),
      placeholder: 'global',
      attrs: { min: 0, step: 1, 'aria-label': `${site.label} minimum temperature` },
      on: {
        input: (e) => {
          clearErrors(pred, input);
          input.nextError?.remove();
          input.nextError = null;
          const v = input.validity;
          if (v.badInput || v.stepMismatch) {
            input.dataset.bad = '1';
            input.classList.add('invalid');
            input.nextError = h('div', { className: 'field-error', textContent: 'Enter a whole number' });
            input.closest('.site').append(input.nextError);
            return;
          }
          delete input.dataset.bad;
          input.classList.remove('invalid');
          doc[site.name] = withMinTemperature(entry(), e.target.value);
          updateBar();
        },
      },
    });
    if (Object.keys(st.errors).some(pred)) input.classList.add('invalid');
    const box = h('input', {
      type: 'checkbox', checked: !!entry().enabled,
      on: { change: (e) => { doc[site.name] = { ...entry(), enabled: e.target.checked }; updateBar(); } },
    });
    return h('div', { className: 'site' },
      h('label', { className: 'site-name' }, box, ' ', site.label, ' ',
        h('span', { className: 'meta', textContent: site.domain })),
      h('span', { className: 'site-min' }, input, h('span', { className: 'suffix', textContent: '°' })),
      errorNodes(pred));
  });
  return [h('div', { className: 'sites' }, rows), errorNodes(exact('sources'))];
}

function renderFilters() {
  const r = st.draft.rules;
  const minPred = exact('rules.min_temperature');
  const ovPred = under('rules.category_overrides');
  const entries = Object.entries(r.category_overrides);
  const unused = st.categories.filter((c) => !(c in r.category_overrides));

  const rows = entries.map(([cat, deg]) => {
    const names = [...new Set([...st.categories, cat])];
    const sel = select(names.map((n) => [n, n, n !== cat && n in r.category_overrides]), cat, (to) => {
      r.category_overrides = Object.fromEntries(Object.entries(r.category_overrides).map(([k, v]) => [k === cat ? to : k, v]));
      touch('filters');
    }, 'Category');
    sel.classList.toggle('invalid', Object.keys(st.errors).some(ovPred));
    return h('div', { className: 'override' }, sel,
      numberInput(deg, (v) => { r.category_overrides[cat] = v; }, { label: `Minimum temperature for ${cat}`, pred: ovPred }),
      h('span', { className: 'suffix', textContent: '°' }),
      h('button', {
        type: 'button', className: 'btn ghost small', textContent: 'Remove', attrs: { 'aria-label': `Remove minimum for ${cat}` },
        on: { click: () => { delete r.category_overrides[cat]; touch('filters'); } },
      }));
  });

  const minTemp = numberInput(r.min_temperature, (v) => { r.min_temperature = v; }, { label: 'Minimum temperature', pred: minPred });
  minTemp.id = 'min-temperature';

  return [
    chipsField({
      key: 'muted_merchants', label: 'Muted shops', list: r.muted_merchants, cls: 'block', placeholder: 'Add a shop',
      section: 'filters', set: (v) => { r.muted_merchants = v; }, pred: under('rules.muted_merchants'),
    }),
    chipsField({
      key: 'muted_categories', label: 'Muted categories', list: r.muted_categories, cls: 'block',
      placeholder: 'Add a category (or pick one seen in your deals)', section: 'filters',
      set: (v) => { r.muted_categories = v; }, pred: under('rules.muted_categories'),
      datalistId: 'category-options', options: st.categories,
    }),
    chipsField({
      key: 'block_keywords', label: 'Block keywords', help: 'Matched anywhere in the title, ignoring case.',
      list: r.block_keywords, cls: 'block', placeholder: 'Add a keyword', section: 'filters',
      set: (v) => { r.block_keywords = v; }, pred: under('rules.block_keywords'),
    }),
    h('div', { className: 'field' },
      h('label', { htmlFor: 'min-temperature', textContent: 'Minimum temperature' }),
      h('div', { className: 'row' }, minTemp,
        h('span', { className: 'suffix', textContent: '°' }),
        h('span', { className: 'help', textContent: 'Cooler trending deals are blocked as “too cold”.' })),
      errorNodes(minPred)),
    h('div', { className: 'field' },
      h('span', { className: 'label', textContent: 'Per-category minimum' }),
      h('div', { className: 'overrides' }, rows,
        h('div', {},
          h('button', {
            type: 'button', className: 'btn small', textContent: '+ Add category minimum', disabled: unused.length === 0,
            on: { click: () => { r.category_overrides[unused[0]] = 0; touch('filters'); } },
          }),
          st.categories.length === 0
            ? h('span', { className: 'help', textContent: ' Categories show up once deals have been seen.' }) : null)),
      errorNodes(ovPred)),
  ];
}

function renderRecurrence() {
  const rc = st.draft.rules.recurrence;
  const seenPred = exact('rules.recurrence.max_seen');
  const windowPred = exact('rules.recurrence.window_days');
  return [h('div', { className: 'field' },
    h('div', { className: 'row' }, 'Block an offer once it was seen ',
      numberInput(rc.max_seen, (v) => { rc.max_seen = v; }, { min: 1, label: 'Times seen before', width: '64px', pred: seenPred }),
      ' times before within ',
      numberInput(rc.window_days, (v) => { rc.window_days = v; }, { min: 1, label: 'Window in days', width: '72px', pred: windowPred }),
      ' days.'),
    h('span', { className: 'help', textContent: 'Watchlist matches are never blocked as recurring.' }),
    errorNodes(seenPred), errorNodes(windowPred))];
}

const FIELD_LABELS = { url: 'Server', topic: 'Topic', priority: 'Priority', chat_id: 'Chat ID', type: 'Type' };

function channelCard(name) {
  const ch = st.draft.channels[name];
  const pred = under(`channels.${name}`);
  const errs = Object.entries(st.errors).filter(([p]) => pred(p));
  const bad = (field) => errs.some(([p]) => p === `channels.${name}.${field}`);
  const text = (field, label) => [
    h('label', { textContent: label }),
    h('input', {
      type: 'text', value: ch[field] || '', className: bad(field) ? 'invalid' : '', attrs: { 'aria-label': `${name} ${label}` },
      on: { input: (e) => { ch[field] = e.target.value; clearErrors(pred, e.target); updateBar(); } },
    }),
  ];
  const fields = ch.type === 'telegram'
    ? text('chat_id', 'Chat ID')
    : [
      ...text('url', 'Server'), ...text('topic', 'Topic'),
      h('label', { textContent: 'Priority' }),
      select(['min', 'low', 'default', 'high', 'urgent'].map((v) => [v, v]), ch.priority || 'default',
        (v) => { ch.priority = v; updateBar(); }, `${name} priority`),
    ];

  const warn = st.warnings[name];
  const status = h('span', { className: 'status-ok', hidden: true });
  const test = h('button', {
    type: 'button', className: 'btn small', textContent: 'Send test', disabled: !!warn,
    on: {
      click: async () => {
        test.disabled = true;
        test.textContent = 'Sending…';
        status.hidden = true;
        let res;
        try {
          res = await testChannel(st.draft.channels[name]);
        } catch (err) {
          const fieldErrs = err.body?.errors;
          res = { ok: false, error: fieldErrs ? Object.values(fieldErrs).join('; ') : err.message };
        }
        test.textContent = 'Send test';
        test.disabled = false;
        status.hidden = false;
        status.className = res.ok ? 'status-ok' : 'status-err';
        status.textContent = res.ok ? '✓ Delivered' : `✗ ${res.error || 'Failed'}`;
      },
    },
  });

  return h('div', { className: `channel${errs.length ? ' invalid' : ''}` },
    h('header', {},
      h('span', { className: 'name', textContent: name }),
      h('span', { className: 'pill', textContent: ch.type === 'telegram' ? 'Telegram' : 'ntfy' }),
      h('span', { className: 'spacer' }),
      h('button', {
        type: 'button', className: 'btn ghost small', textContent: 'Remove', attrs: { 'aria-label': `Remove channel ${name}` },
        on: {
          click: () => {
            delete st.draft.channels[name];
            for (const rule of st.draft.notify) rule.channels = removeFrom(rule.channels, name);
            st.draft.digest.channels = removeFrom(st.draft.digest.channels, name);
            touch('notifications');
          },
        },
      })),
    h('div', { className: 'grid2' }, fields),
    warn ? h('span', { className: 'status-warn', textContent: `⚠ ${warn}` }) : null,
    errs.map(([p, msg]) => {
      shown.add(p);
      const field = p.split('.').pop();
      const n = h('div', { className: 'field-error', textContent: p === `channels.${name}` ? msg : `${FIELD_LABELS[field] || field}: ${msg}` });
      n._pred = pred;
      return n;
    }),
    h('div', { className: 'row' }, test, status));
}

function addChannelTile() {
  if (!st.addingChannel) {
    return h('button', {
      type: 'button', className: 'add-channel',
      on: { click: () => { st.addingChannel = true; touch('notifications'); } },
    }, '+ Add channel', h('span', { className: 'help', textContent: 'ntfy or Telegram' }));
  }
  const add = (type) => {
    const name = nextChannelName(st.draft.channels, type);
    st.draft.channels[name] = type === 'ntfy'
      ? { type, url: '', topic: '', priority: '' } : { type, chat_id: '' };
    st.addingChannel = false;
    touch('notifications');
  };
  return h('div', { className: 'add-channel' },
    h('span', { textContent: 'Channel type' }),
    h('div', { className: 'pick' },
      h('button', { type: 'button', className: 'btn small', textContent: 'ntfy', on: { click: () => add('ntfy') } }),
      h('button', { type: 'button', className: 'btn small', textContent: 'Telegram', on: { click: () => add('telegram') } }),
      h('button', { type: 'button', className: 'btn ghost small', textContent: 'Cancel', on: { click: () => { st.addingChannel = false; touch('notifications'); } } })));
}

function ruleRow(rule, i) {
  const pred = under(`notify[${i}]`);
  const errs = errorNodes(pred);
  const isTemp = typeof rule.when === 'object';
  const free = Object.keys(st.draft.channels).filter((c) => !rule.channels.includes(c));
  const picker = h('select', {
    attrs: { 'aria-label': 'Add a channel to this rule' }, style: 'width:auto',
    on: {
      change: (e) => {
        if (!e.target.value) return;
        rule.channels = [...rule.channels, e.target.value];
        clearErrors(pred);
        touch('notifications');
      },
    },
  }, h('option', { value: '', textContent: '+ channel' }), free.map((c) => h('option', { value: c, textContent: c })));

  return h('div', { className: `rule${errs.length ? ' invalid' : ''}` },
    h('span', { className: 'kw', textContent: 'When' }),
    select([['watchlist', 'a watchlist deal passes'], ['temperature', 'a deal reaches a temperature']],
      isTemp ? 'temperature' : 'watchlist', (v) => {
        rule.when = v === 'watchlist' ? 'watchlist' : { min_temperature: 500 };
        clearErrors(pred);
        touch('notifications');
      }, 'Condition'),
    isTemp ? numberInput(rule.when.min_temperature, (v) => { rule.when = { min_temperature: v }; }, { label: 'Temperature', width: '80px', pred }) : null,
    isTemp ? h('span', { className: 'suffix', textContent: '°' }) : null,
    h('span', { className: 'kw', textContent: 'send to' }),
    rule.channels.map((c) => h('span', { className: 'chip' }, c, h('button', {
      type: 'button', textContent: '×', attrs: { 'aria-label': `Stop sending to ${c}` },
      on: { click: () => { rule.channels = removeFrom(rule.channels, c); clearErrors(pred); touch('notifications'); } },
    }))),
    free.length ? picker : null,
    h('span', { className: 'spacer' }),
    h('button', {
      type: 'button', className: 'btn ghost small', textContent: 'Remove',
      on: {
        click: () => {
          st.draft.notify.splice(i, 1);
          clearErrors((p) => p.startsWith('notify[')); // indices shift
          touch('notifications');
        },
      },
    }),
    errs.length ? h('div', { style: 'flex-basis:100%' }, errs) : null);
}

function digestBlock() {
  const d = st.draft.digest;
  const pred = under('digest');
  const bad = (field) => Object.keys(st.errors).some((p) => p === `digest.${field}` || p.startsWith(`digest.${field}[`));
  const free = Object.keys(st.draft.channels).filter((c) => !d.channels.includes(c));
  const status = h('span', { className: 'status-ok', hidden: true });
  const test = h('button', {
    type: 'button', className: 'btn small', textContent: 'Send test digest',
    on: {
      click: async () => {
        test.disabled = true;
        test.textContent = 'Sending…';
        status.hidden = true;
        let text;
        let ok = false;
        try {
          const results = await testDigest();
          ok = results.every((r) => r.ok);
          text = results.map((r) => `${r.channel}: ${r.ok ? '✓ delivered' : `✗ ${r.error || 'failed'}`}`).join(' · ');
        } catch (err) {
          text = `✗ ${err.message}`;
        }
        test.textContent = 'Send test digest';
        test.disabled = false;
        status.hidden = false;
        status.className = ok ? 'status-ok' : 'status-err';
        status.textContent = text;
      },
    },
  });
  return h('div', { className: 'field' },
    h('span', { className: 'label', textContent: 'Daily digest' }),
    h('p', { className: 'help', textContent: 'Once a day: your watchlist hits and the top deals that are new since the previous digest. Skipped when there is nothing new. The instant notifications above keep working.' }),
    h('label', { className: 'row' },
      h('input', { type: 'checkbox', checked: d.enabled, on: { change: (e) => { d.enabled = e.target.checked; clearErrors(pred); updateBar(); } } }),
      ' Send a daily digest'),
    h('div', { className: 'row' },
      h('span', { className: 'kw', textContent: 'at' }),
      h('input', {
        type: 'time', value: d.time, className: bad('time') ? 'invalid' : '', attrs: { 'aria-label': 'Digest time' }, style: 'width:auto',
        on: { input: (e) => { d.time = e.target.value; clearErrors(pred, e.target); updateBar(); } },
      }),
      h('span', { className: 'kw', textContent: 'time zone' }),
      h('input', {
        type: 'text', value: d.timezone, className: bad('timezone') ? 'invalid' : '', style: 'width:14em',
        attrs: { 'aria-label': 'Digest time zone', list: 'timezones' },
        on: { input: (e) => { d.timezone = e.target.value; clearErrors(pred, e.target); updateBar(); } },
      }),
      h('datalist', { id: 'timezones' }, TIMEZONES.map((z) => h('option', { value: z }))),
      h('span', { className: 'kw', textContent: 'top' }),
      numberInput(d.top, (v) => { d.top = v; }, { min: 1, label: 'Deals in the digest', width: '70px', pred: (p) => p === 'digest.top' })),
    h('div', { className: 'row' },
      h('span', { className: 'kw', textContent: 'send to' }),
      d.channels.map((c) => h('span', { className: 'chip' }, c, h('button', {
        type: 'button', textContent: '×', attrs: { 'aria-label': `Stop sending the digest to ${c}` },
        on: { click: () => { d.channels = removeFrom(d.channels, c); clearErrors(pred); touch('notifications'); } },
      }))),
      free.length ? h('select', {
        attrs: { 'aria-label': 'Add a channel to the digest' }, style: 'width:auto',
        on: { change: (e) => { if (!e.target.value) return; d.channels = [...d.channels, e.target.value]; clearErrors(pred); touch('notifications'); } },
      }, h('option', { value: '', textContent: '+ channel' }), free.map((c) => h('option', { value: c, textContent: c }))) : null),
    errorNodes(pred),
    h('div', { className: 'row' }, test, h('span', { className: 'help', textContent: 'Uses the saved settings.' }), status));
}

function judgeBlock() {
  const j = st.draft.judge;
  const pred = under('judge');
  const bad = (field) => `judge.${field}` in st.errors;
  const env = st.judgeEnv;
  const status = h('span', { className: 'status-ok', hidden: true });
  const test = h('button', {
    type: 'button', className: 'btn small', textContent: 'Test judge',
    on: {
      click: async () => {
        test.disabled = true;
        test.textContent = 'Testing…';
        status.hidden = true;
        let res;
        try {
          res = await testJudge(st.draft.judge);
        } catch (err) {
          const fieldErrs = err.body?.errors;
          if (fieldErrs) {
            // Same shape as a failed save: show each message under its field.
            Object.assign(st.errors, fieldErrs);
            touch('notifications');
            return;
          }
          res = { ok: false, error: err.message };
        }
        test.textContent = 'Test judge';
        test.disabled = false;
        status.hidden = false;
        status.className = res.ok ? 'status-ok' : 'status-err';
        status.textContent = res.ok ? `${sentence(res.rating)}: ${sentence(res.reason)}` : `Failed: ${res.error || 'unknown error'}`;
      },
    },
  });
  return h('div', { className: 'field' },
    h('span', { className: 'label', textContent: 'LLM judge' }),
    h('p', { className: 'help', textContent: 'Asks a language model for a one-line opinion on each deal that passed your rules, for example hidden fees. Opinions show on the deal and in the digest; they never block a deal or send a notification. Each passed deal is judged once; the daily limit caps how many requests are made.' }),
    h('label', { className: 'row' },
      h('input', { type: 'checkbox', checked: j.enabled, on: { change: (e) => { j.enabled = e.target.checked; clearErrors(pred); updateBar(); } } }),
      ' Ask an LLM about passed deals'),
    h('div', { className: 'row' },
      h('span', { className: 'kw', textContent: 'Model' }),
      h('input', {
        type: 'text', value: j.model, className: bad('model') ? 'invalid' : '', style: 'width:18em',
        placeholder: 'e.g. a model id from your provider', attrs: { 'aria-label': 'Judge model' },
        on: { input: (e) => { j.model = e.target.value; clearErrors(exact('judge.model'), e.target); updateBar(); } },
      }),
      h('span', { className: 'kw', textContent: 'Requests per day' }),
      numberInput(j.daily_limit, (v) => { j.daily_limit = v; }, { min: 1, max: 5000, label: 'Judge requests per day', width: '80px', pred: exact('judge.daily_limit') })),
    h('div', {},
      h('label', { className: 'kw', textContent: 'Notes for the judge' }),
      h('textarea', {
        value: j.instructions, rows: 2, maxLength: 500, className: bad('instructions') ? 'invalid' : '', style: 'width:100%',
        placeholder: 'e.g. I fly from Berlin. Answer in German.', attrs: { 'aria-label': 'Notes for the judge' },
        on: { input: (e) => { j.instructions = e.target.value; clearErrors(exact('judge.instructions'), e.target); updateBar(); } },
      })),
    h('p', { className: 'help', textContent: `Requests go to ${env.url || 'the configured server'}. ${env.key_set ? 'API key set.' : 'No API key set (fine for a local server).'}` }),
    errorNodes(pred),
    h('div', { className: 'row' }, test, h('span', { className: 'help', textContent: 'Uses the values above, saved or not.' }), status));
}

function renderNotifications() {
  const names = Object.keys(st.draft.channels);
  return [
    h('div', { className: 'field' },
      h('span', { className: 'label', textContent: 'Channels' }),
      h('div', { className: 'channels' }, names.map(channelCard), addChannelTile()),
      errorNodes(exact('channels'))),
    h('div', { className: 'field' },
      h('span', { className: 'label', textContent: 'When to notify' }),
      h('div', { className: 'rules' }, st.draft.notify.map(ruleRow),
        h('div', {}, h('button', {
          type: 'button', className: 'btn small', textContent: '+ Add rule',
          on: { click: () => { st.draft.notify.push({ when: 'watchlist', channels: [] }); touch('notifications'); } },
        })))),
    digestBlock(),
    judgeBlock(),
  ];
}

function renderClicks() {
  if (!st.clicks.length) {
    return [h('p', { className: 'empty', textContent: 'No clicks yet. Hide or Junk a deal in the inbox and it shows up here.' })];
  }
  return [h('ul', { className: 'clicks' }, st.clicks.map((c) => h('li', {},
    h('span', { className: 'what' },
      h('span', { className: 'kind', textContent: clickKind(c.kind) }),
      c.url
        ? h('a', { href: c.url, target: '_blank', rel: 'noopener noreferrer', textContent: c.label })
        : h('span', { textContent: c.label })),
    h('span', { className: 'when', textContent: ago(c.created_at) }),
    h('button', { type: 'button', className: 'btn small', textContent: 'Undo', on: { click: (e) => onUndoClick(c, e.currentTarget) } }))))];
}

function renderHistory() {
  const items = st.history.map((e) => h('li', {},
    h('span', { className: 'summary', textContent: e.summary?.length ? e.summary.join('; ') : 'Settings saved' }),
    h('span', { className: 'when', textContent: new Date(e.created_at).toLocaleString() })));
  return [
    st.history.length ? h('ul', { className: 'history' }, items) : h('p', { className: 'empty', textContent: 'No changes yet.' }),
    h('div', { className: 'row', style: 'margin-top:10px' }, h('button', {
      type: 'button', className: 'btn', textContent: 'Undo last change', disabled: st.history.length <= 1,
      on: { click: onUndo },
    })),
  ];
}

// ---- actions

let toastTimer;
function toast(text, { error = false, ms = 2500 } = {}) {
  const t = $('#toast');
  t.textContent = text;
  t.classList.toggle('err', error);
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), ms);
}

function banner(text, buttonLabel, onClick) {
  const b = $('#page-error');
  b.replaceChildren(h('span', { textContent: text }),
    buttonLabel ? h('button', { type: 'button', className: 'btn small', textContent: buttonLabel, on: { click: onClick } }) : null);
  b.hidden = false;
}

const hideBanner = () => { $('#page-error').hidden = true; };

async function reload() {
  const [doc, suggestions, history] = await Promise.all([getSettings(), getSuggestions(), getHistory()]);
  adopt(doc);
  st.suggestions = suggestions;
  st.history = history;
}

function adopt(doc) {
  st.saved = doc.settings;
  st.sites = doc.sites || st.sites;
  st.draft = clone(doc.settings);
  st.version = doc.version;
  st.warnings = doc.warnings || {};
  st.judgeEnv = doc.judge_env || {};
  st.errors = {};
  st.stale = false;
  hideBanner();
}

async function onUndoClick(click, btn) {
  btn.disabled = true;
  try {
    await deleteAction(click.id);
    st.clicks = st.clicks.filter((c) => c.id !== click.id);
    render('clicks');
    toast('Undone');
  } catch (err) {
    btn.disabled = false;
    toast(err.message, { error: true });
    return;
  }
  // The undo changes which mutes are suggested; a failed refresh can wait for a reload.
  try { st.suggestions = await getSuggestions(); render('suggestions'); } catch { /* stale until the next load */ }
}

async function onApply(s, applyBtn, dismissBtn) {
  applyBtn.disabled = true;
  dismissBtn.disabled = true;
  const keep = changeCount(st.saved, st.draft) > 0 ? st.draft : null;
  try {
    await applySuggestion(s.kind, s.value);
    await reload();
    if (keep) {
      // Keep the user's unsaved edits; the applied mute is already saved, so add it to the draft too.
      const key = s.kind === 'mute_merchant' ? 'muted_merchants' : 'muted_categories';
      keep.rules[key] = addTo(keep.rules[key], s.value);
      st.draft = keep;
    }
    renderAll();
    toast(`Muted ${s.kind === 'mute_merchant' ? 'shop' : 'category'} “${s.value}”`);
  } catch (err) {
    toast(err.message, { error: true });
    applyBtn.disabled = false;
    dismissBtn.disabled = false;
  }
}

async function onDismiss(s, applyBtn, dismissBtn) {
  applyBtn.disabled = true;
  dismissBtn.disabled = true;
  try {
    st.suggestions = await dismissSuggestion(s.kind, s.value);
    render('suggestions');
  } catch (err) {
    toast(err.message, { error: true });
    applyBtn.disabled = false;
    dismissBtn.disabled = false;
  }
}

function showStale() {
  st.stale = true;
  banner('Someone else saved the settings. Reload to see them (your unsaved changes will be lost).', 'Reload', () => {
    st.draft = clone(st.saved); // so the unload warning doesn't fire
    location.reload();
  });
}

async function onUndo() {
  if (changeCount(st.saved, st.draft) > 0 && !confirm('Undoing discards your unsaved changes. Continue?')) return;
  try {
    await undoSettings(st.version);
    await reload();
    renderAll();
    toast('Undone');
  } catch (err) {
    if (err.status === 409 && err.body?.code === 'stale') {
      showStale();
    } else {
      toast(err.message || 'Nothing to undo', { error: true });
    }
  }
}

async function onSave() {
  const bad = $('input[data-bad]');
  if (bad) {
    bad.scrollIntoView({ block: 'center', behavior: 'smooth' });
    bad.focus();
    toast('Some settings need fixing', { error: true });
    return;
  }
  const save = $('#save');
  save.disabled = true;
  hideBanner();
  try {
    const res = await putSettings(st.version, st.draft);
    adopt(res);
    renderAll();
    const msg = `Saved · ${res.rechecked ?? 0} recent deals re-checked`;
    toast(res.warning ? `${msg}. ${res.warning}` : msg, { ms: res.warning ? 6000 : 2500 });
    // The save worked; a failed refresh of the side data must not look like a failed save.
    try {
      const [history, suggestions] = await Promise.all([getHistory(), getSuggestions()]);
      st.history = history;
      st.suggestions = suggestions;
      render('history');
      render('suggestions');
    } catch { /* stale until the next load */ }
  } catch (err) {
    if (err.status === 422 && err.body?.errors) {
      st.errors = err.body.errors;
      renderAll();
      const rest = Object.entries(st.errors).filter(([p]) => !shown.has(p));
      if (rest.length) banner(rest.map(([p, m]) => `${p}: ${m}`).join(' · '));
      $('.field-error')?.scrollIntoView({ block: 'center', behavior: 'smooth' });
      toast('Some settings need fixing', { error: true });
    } else if (err.status === 409 && err.body?.code === 'stale') {
      showStale();
    } else {
      banner(`Couldn't save: ${err.message}`);
    }
  } finally {
    save.disabled = false;
  }
}

// ---- wiring

function initNav() {
  const links = [...document.querySelectorAll('.sidenav a')];
  const mark = (id) => links.forEach((l) => l.classList.toggle('on', l.getAttribute('href') === `#${id}`));
  links.forEach((l) => l.addEventListener('click', (e) => {
    e.preventDefault();
    const id = l.getAttribute('href').slice(1);
    document.getElementById(id).scrollIntoView({ behavior: 'smooth' });
    mark(id);
  }));
  const io = new IntersectionObserver(
    (entries) => entries.forEach((en) => {
      // At the very top the short first card sits above the observed band; keep it highlighted.
      if (en.isIntersecting) mark(scrollY < 40 ? 'suggestions' : en.target.id);
    }),
    { rootMargin: '-40% 0px -55% 0px' },
  );
  document.querySelectorAll('.sections .card').forEach((c) => io.observe(c));
}

async function init() {
  initTheme($('#theme-toggle'));
  initNav();
  $('#save').addEventListener('click', onSave);
  $('#discard').addEventListener('click', async () => {
    st.addingChannel = false;
    if (st.stale) {
      // After a 409 our version is out of date, so discarding must fetch the current one.
      try {
        await reload();
        st.stale = false;
        hideBanner();
        renderAll();
      } catch (err) {
        toast(`Couldn't reload: ${err.message}`, { error: true });
      }
      return;
    }
    st.draft = clone(st.saved);
    st.errors = {};
    hideBanner();
    renderAll();
  });
  window.addEventListener('beforeunload', (e) => {
    if (st.draft && changeCount(st.saved, st.draft) > 0) { e.preventDefault(); e.returnValue = ''; }
  });
  try {
    const [doc, suggestions, history, categories, clicks] = await Promise.all([getSettings(), getSuggestions(), getHistory(), getCategories(), getActions()]);
    adopt(doc);
    st.clicks = clicks;
    st.suggestions = suggestions;
    st.history = history;
    st.categories = categories;
    renderAll();
  } catch (err) {
    banner(`Couldn't load the settings: ${err.message}`, 'Retry', () => location.reload());
  }
}

init();
