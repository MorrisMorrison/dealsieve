import * as api from './api.js';
import {
  fillCategories, renderCounts, renderDetail, renderError, renderList, renderRules, renderStatus,
} from './render.js';
import { initTheme } from './theme.js';

const list = document.getElementById('list');
const detail = document.getElementById('detail');
const dealsPanel = document.getElementById('deals-panel');
const rulesPanel = document.getElementById('rules-panel');
const toolbar = document.getElementById('toolbar');
const rulesToggle = document.getElementById('rules-toggle');
const category = document.getElementById('category');
const search = document.getElementById('search');
const tabs = [...document.querySelectorAll('[role=tab]')];
const narrow = matchMedia('(max-width: 860px)');

const state = { view: 'passed', deals: [], selectedId: null, rulesOpen: false };

let loadSeq = 0; // a slow response must not overwrite a newer load

async function load() {
  const seq = ++loadSeq;
  try {
    if (state.rulesOpen) {
      const rules = await api.getRules();
      if (seq !== loadSeq) return;
      renderRules(rulesPanel, rules, undo);
      return;
    }
    const { deals, counts } = await api.getDeals(state.view);
    if (seq !== loadSeq) return;
    state.deals = deals;
    renderCounts(counts);
    fillCategories(category, deals);
    show();
  } catch (err) {
    if (seq !== loadSeq) return;
    renderError(state.rulesOpen ? rulesPanel : list, err);
  }
}

function visible() {
  const q = search.value.trim().toLowerCase();
  const cat = category.value;
  return state.deals.filter((d) => (!cat || d.category === cat) && (!q || d.title.toLowerCase().includes(q)));
}

function show() {
  const deals = visible();
  if (!deals.some((d) => d.id === state.selectedId)) state.selectedId = deals[0]?.id ?? null;
  renderList(list, deals, state.selectedId, select);
  showDetail(deals);
}

function showDetail(deals = visible()) {
  const deal = deals.find((d) => d.id === state.selectedId);
  renderDetail(detail, deal, { onAction: act, onBack: closeSheet });
  const item = deal && list.querySelector(`[data-id="${CSS.escape(deal.id)}"]`);
  if (item) list.setAttribute('aria-activedescendant', item.id);
  else list.removeAttribute('aria-activedescendant');
}

function select(id, { open = false } = {}) {
  state.selectedId = id;
  for (const item of list.querySelectorAll('[role=option]')) {
    const on = item.dataset.id === id;
    item.setAttribute('aria-selected', String(on));
    if (on) item.scrollIntoView({ block: 'nearest' });
  }
  showDetail();
  if (open && narrow.matches) document.body.classList.add('sheet-open');
}

function closeSheet() {
  document.body.classList.remove('sheet-open');
}

function move(step) {
  const deals = visible();
  if (deals.length === 0) return;
  const i = deals.findIndex((d) => d.id === state.selectedId);
  const next = Math.min(deals.length - 1, Math.max(0, (i < 0 ? -1 : i) + step));
  select(deals[next].id);
}

async function act(kind, deal) {
  // Keep the user's place: after the deal leaves this view, select its neighbour.
  const deals = visible();
  const i = deals.findIndex((d) => d.id === deal.id);
  const neighbour = deals[i + 1] ?? deals[i - 1];
  try {
    await api.addAction(kind, deal.id);
    // In the Blocked tab the deal stays listed, so the selection stays on it.
    if (state.view !== 'blocked') state.selectedId = neighbour?.id ?? null;
    closeSheet();
    await load();
  } catch (err) {
    renderError(list, err);
  }
}

async function undo(id) {
  try {
    await api.deleteAction(id);
    await load();
  } catch (err) {
    renderError(rulesPanel, err);
  }
}

async function refreshStatus() {
  try {
    renderStatus(document.getElementById('status'), await api.getStatus());
  } catch {
    // The list shows request errors; the status line keeps its last value.
  }
}

function setView(tab) {
  for (const t of tabs) {
    const on = t === tab;
    t.setAttribute('aria-selected', String(on));
    t.tabIndex = on ? 0 : -1;
  }
  dealsPanel.setAttribute('aria-labelledby', tab.id);
  state.view = tab.dataset.view;
  state.selectedId = null;
  closeSheet();
  load();
}

function setRulesOpen(open) {
  state.rulesOpen = open;
  rulesToggle.setAttribute('aria-pressed', String(open));
  rulesPanel.hidden = !open;
  dealsPanel.hidden = open;
  toolbar.hidden = open;
  closeSheet();
  load();
}

for (const tab of tabs) {
  tab.addEventListener('click', () => setView(tab));
  tab.addEventListener('keydown', (e) => {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    const i = tabs.indexOf(tab) + (e.key === 'ArrowRight' ? 1 : -1);
    const next = tabs[(i + tabs.length) % tabs.length];
    next.focus();
    setView(next);
  });
}

document.addEventListener('keydown', (e) => {
  if (state.rulesOpen || e.altKey || e.ctrlKey || e.metaKey) return;
  if (e.target.closest('input, select, textarea, [role=tab]')) return;
  if (e.key === 'ArrowDown' || e.key === 'j') { e.preventDefault(); move(1); }
  else if (e.key === 'ArrowUp' || e.key === 'k') { e.preventDefault(); move(-1); }
  else if (e.key === 'Enter' && !e.repeat && state.selectedId && !e.target.closest('button, a')) {
    const deal = state.deals.find((d) => d.id === state.selectedId);
    if (deal) window.open(deal.url, '_blank', 'noopener,noreferrer');
  } else if (e.key === 'Escape') closeSheet();
});

rulesToggle.addEventListener('click', () => setRulesOpen(!state.rulesOpen));
category.addEventListener('change', show);
search.addEventListener('input', show);
narrow.addEventListener('change', closeSheet);
initTheme(document.getElementById('theme-toggle'));

load();
refreshStatus();
setInterval(() => { if (!state.rulesOpen) load(); }, 120_000);
setInterval(refreshStatus, 60_000);
