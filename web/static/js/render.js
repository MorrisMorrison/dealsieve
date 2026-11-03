import { sparkPoints } from './sparkline.js';
import { ago } from './format.js';

const ACTION_LABELS = {
  hide_deal: 'Hidden deal',
  junk: 'Junk pattern',
  mute_category: 'Muted category',
  mute_merchant: 'Muted merchant',
};

export function renderDeals(container, deals, { blockedView, onAction }) {
  if (deals.length === 0) {
    container.replaceChildren(el('p', 'note', 'Nothing here right now.'));
    return;
  }
  const template = document.getElementById('deal-card');
  container.replaceChildren(...deals.map((deal) => dealCard(template, deal, blockedView, onAction)));
}

function dealCard(template, deal, blockedView, onAction) {
  const card = template.content.firstElementChild.cloneNode(true);
  const img = card.querySelector('.deal-img');
  if (deal.image_url) img.src = deal.image_url;
  else img.remove();

  const link = card.querySelector('.deal-title');
  link.href = deal.url;
  link.textContent = deal.title;
  card.querySelector('.deal-price').textContent = deal.price;
  card.querySelector('.deal-merchant').textContent = deal.merchant;
  card.querySelector('.deal-category').textContent = deal.category;
  card.querySelector('.deal-temp').textContent = deal.temperature == null ? '' : `${deal.temperature}°`;
  card.querySelector('.spark polyline').setAttribute('points', sparkPoints(deal.temperatures));

  const chips = card.querySelector('.chips');
  if (deal.in_trending) chips.append(chip('trending'));
  deal.reasons.forEach((reason, i) => {
    chips.append(chip(reason, deal.decision === 'block' && i === 0 ? 'chip-block' : ''));
  });

  for (const button of card.querySelectorAll('.actions button')) {
    const kind = button.dataset.kind;
    if ((kind === 'mute_category' && !deal.category) || (kind === 'mute_merchant' && !deal.merchant)) {
      button.remove();
      continue;
    }
    button.addEventListener('click', () => onAction(kind, deal));
  }
  if (blockedView) card.classList.add('deal-blocked');
  return card;
}

export function renderRules(container, data, onUndo) {
  const list = el('ul', 'action-list');
  if (data.actions.length === 0) list.append(el('li', 'note', 'No clicks yet.'));
  for (const action of data.actions) {
    const undo = el('button', '', 'Undo');
    undo.type = 'button';
    undo.addEventListener('click', () => onUndo(action.id));
    const item = el('li');
    item.append(el('span', '', `${ACTION_LABELS[action.kind] ?? action.kind}: ${action.value}`), undo);
    list.append(item);
  }
  container.replaceChildren(
    el('h2', '', 'Active rules (config.yaml, with defaults)'),
    el('pre', 'rules-yaml', JSON.stringify({ rules: data.rules, watchlist: data.watchlist }, null, 2)),
    el('h2', '', 'Your clicks'),
    list,
  );
}

export function renderStale(banner, feeds) {
  const stale = feeds.filter((f) => f.stale);
  banner.hidden = stale.length === 0;
  banner.textContent = stale
    .map((f) => `${f.feed}: last successful poll ${ago(f.last_success)}${f.last_error ? ` (${f.last_error})` : ''}`)
    .join(' · ');
}

export function renderError(container, err) {
  container.replaceChildren(el('p', 'error', `Something went wrong: ${err.message}`));
}

export function fillCategories(select, deals) {
  const current = select.value;
  const categories = [...new Set(deals.map((d) => d.category).filter(Boolean))]
    .sort((a, b) => a.localeCompare(b, 'de'));
  select.replaceChildren(new Option('All categories', ''), ...categories.map((c) => new Option(c, c)));
  select.value = categories.includes(current) ? current : '';
}

function chip(text, extra = '') {
  return el('li', extra ? `chip ${extra}` : 'chip', text);
}

function el(tag, className = '', text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
