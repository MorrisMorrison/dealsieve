import * as api from './api.js';
import { fillCategories, renderDeals, renderError, renderRules, renderStale } from './render.js';
import { initTheme } from './theme.js';

const content = document.getElementById('content');
const filters = document.getElementById('filters');
const category = document.getElementById('category');
const search = document.getElementById('search');
const tabs = document.querySelectorAll('[role=tab]');

const state = { view: 'passed', deals: [] };

let loadSeq = 0; // a slow response must not overwrite a newer load

async function load() {
  const seq = ++loadSeq;
  try {
    if (state.view === 'rules') {
      filters.hidden = true;
      const rules = await api.getRules();
      if (seq !== loadSeq) return;
      renderRules(content, rules, undo);
      return;
    }
    filters.hidden = false;
    const deals = await api.getDeals(state.view);
    if (seq !== loadSeq) return;
    state.deals = deals;
    fillCategories(category, state.deals);
    show();
  } catch (err) {
    if (seq !== loadSeq) return;
    renderError(content, err);
  }
}

function show() {
  const q = search.value.trim().toLowerCase();
  const cat = category.value;
  const deals = state.deals.filter((d) => (!cat || d.category === cat) && (!q || d.title.toLowerCase().includes(q)));
  renderDeals(content, deals, { blockedView: state.view === 'blocked', onAction: act });
}

async function act(kind, deal) {
  try {
    await api.addAction(kind, deal.id);
    await load();
  } catch (err) {
    renderError(content, err);
  }
}

async function undo(id) {
  try {
    await api.deleteAction(id);
    await load();
  } catch (err) {
    renderError(content, err);
  }
}

async function refreshStatus() {
  try {
    renderStale(document.getElementById('stale'), await api.getStatus());
  } catch {
    // The deal list shows errors; the banner just stays as it was.
  }
}

for (const tab of tabs) {
  tab.addEventListener('click', () => {
    for (const t of tabs) t.setAttribute('aria-selected', String(t === tab));
    state.view = tab.dataset.view;
    load();
  });
}
category.addEventListener('change', show);
search.addEventListener('input', show);
filters.addEventListener('submit', (e) => e.preventDefault());
initTheme(document.getElementById('theme-toggle'));

load();
refreshStatus();
setInterval(() => { if (state.view !== 'rules') load(); }, 120_000);
setInterval(refreshStatus, 60_000);
