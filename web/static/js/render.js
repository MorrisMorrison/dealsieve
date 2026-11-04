import { areaPoints, sparkPoints } from './sparkline.js';
import { age, ago, clock, heat, monthYear, ratingMark, sentence } from './format.js';
import { historyChip, historyLine } from './history.js';

// A blocked deal the server marks as blocked by a click (deal.undo) can be undone on the spot.
const UNDO_LABELS = { hide_deal: 'Unhide', junk: 'Not junk' };

const FEED_LABELS = { trending: 'trending', new: 'new deals' };

export function renderList(list, deals, selectedId, onSelect, { showSource = false } = {}) {
  if (deals.length === 0) {
    list.replaceChildren(el('li', 'empty', 'Nothing here right now.'));
    return;
  }
  const template = document.getElementById('item-template');
  list.replaceChildren(...deals.map((deal) => {
    const item = template.content.firstElementChild.cloneNode(true);
    item.id = `deal-${cssSafe(deal.id)}`;
    item.dataset.id = deal.id;
    item.setAttribute('aria-selected', String(deal.id === selectedId));
    item.classList.toggle('blocked', deal.decision === 'block');

    const thumb = item.querySelector('.thumb');
    const initial = (deal.merchant || deal.title || '?').slice(0, 1);
    if (deal.image_url) {
      const img = el('img');
      img.alt = '';
      img.loading = 'lazy';
      img.src = deal.image_url;
      img.addEventListener('error', () => img.replaceWith(initial));
      thumb.append(img);
    } else {
      thumb.textContent = initial;
    }

    item.querySelector('.title').textContent = deal.title;
    const mark = ratingMark(deal.opinion?.rating);
    if (mark) {
      const badge = item.querySelector('.judge-mark');
      badge.hidden = false;
      badge.textContent = mark.symbol;
      badge.classList.add(mark.className);
      const label = `Judge: ${mark.label} — ${deal.opinion.reason}`;
      badge.title = label;
      badge.setAttribute('aria-label', label);
    }
    item.querySelector('.price').textContent = deal.price;
    item.querySelector('.merchant').textContent = deal.merchant;
    fillSource(item.querySelector('.source'), deal, showSource);
    const chip = item.querySelector('.chip');
    if (deal.decision === 'block') {
      chip.textContent = sentence(deal.reasons[0] ?? 'blocked');
      chip.classList.add('block');
    } else if (deal.watchlist_hit) {
      chip.textContent = '★ watchlist';
      chip.classList.add('watch');
    }
    const history = historyChip(deal.price_history);
    if (history) {
      const priceChip = item.querySelector('.price-chip');
      priceChip.textContent = history.text;
      priceChip.classList.add(history.cls);
    }
    const temp = item.querySelector('.temp');
    temp.textContent = deal.temperature == null ? '' : `${deal.temperature}°`;
    if (heat(deal.temperature)) temp.classList.add(heat(deal.temperature));
    item.querySelector('.when').textContent = age(deal.first_seen);

    item.addEventListener('click', () => onSelect(deal.id, { open: true }));
    return item;
  }));
}

export function renderDetail(panel, deal, { onAction, onUndo, onBack, showSource = false }) {
  if (!deal) {
    panel.replaceChildren();
    return;
  }
  const node = document.getElementById('detail-template').content.firstElementChild.cloneNode(true);
  node.querySelector('[data-back]').addEventListener('click', onBack);
  node.querySelector('.detail-title').textContent = deal.title;
  node.querySelector('.detail-price .price').textContent = deal.price;
  node.querySelector('.detail-price .merchant').textContent = deal.merchant ? `at ${deal.merchant}` : '';

  fillSource(node.querySelector('.source'), deal, showSource);

  const level = heat(deal.temperature);
  const temp = node.querySelector('.heat .temp');
  temp.textContent = deal.temperature == null ? 'unknown' : `${deal.temperature}°`;
  if (level) temp.classList.add(level);
  const spark = node.querySelector('.bigspark');
  if (level) spark.classList.add(level);
  spark.querySelector('polyline').setAttribute('points', sparkPoints(deal.temperatures, 100, 40));
  spark.querySelector('polygon').setAttribute('points', areaPoints(deal.temperatures, 100, 40));
  node.querySelector('.first-seen').textContent = `first seen ${clock(deal.first_seen)}`;
  // A curve needs two readings; setAttribute because SVG elements have no .hidden property.
  if (deal.temperatures.length < 2) {
    spark.setAttribute('hidden', '');
    node.querySelector('.heat-now').textContent = 'curve after the next poll';
  }

  renderHistory(node.querySelector('.history'), deal.price_history);

  const blocked = deal.decision === 'block';
  node.querySelector('.why-label').textContent = blocked ? 'Why it’s blocked' : 'Why it’s here';
  const why = node.querySelector('.why');
  deal.reasons.forEach((reason, i) => {
    const li = el('li', '', sentence(reason));
    if (blocked && i === 0) li.classList.add('no');
    else if (reason.startsWith('watchlist:')) li.classList.add('watch');
    why.append(li);
  });
  if (deal.reasons.length === 0) why.append(el('li', '', 'Passed every rule.'));

  const mark = ratingMark(deal.opinion?.rating);
  if (mark) {
    const sec = node.querySelector('.judge');
    sec.hidden = false;
    const rating = sec.querySelector('.judge-rating');
    rating.textContent = sentence(mark.label);
    rating.classList.add(mark.className);
    sec.querySelector('.judge-reason').textContent = deal.opinion.reason;
    sec.querySelector('.judge-meta').textContent = `${deal.opinion.model} · an LLM’s opinion, it can be wrong`;
  }

  node.querySelector('.category').textContent = deal.category || '—';
  const feeds = [deal.in_trending && FEED_LABELS.trending, deal.in_new && FEED_LABELS.new].filter(Boolean);
  node.querySelector('.feeds').textContent = feeds.join(', ') || '—';

  node.querySelector('.btn.primary').href = deal.url;
  const undoKind = blocked ? deal.undo?.kind : null;
  if (UNDO_LABELS[undoKind]) {
    const button = el('button', 'btn', UNDO_LABELS[undoKind]);
    button.type = 'button';
    button.addEventListener('click', () => onUndo(undoKind, deal));
    node.querySelector('.btn.primary').after(button);
  }
  for (const button of node.querySelectorAll('[data-kind]')) {
    const kind = button.dataset.kind;
    if ((kind === 'mute_category' && !deal.category) || (kind === 'mute_merchant' && !deal.merchant)) {
      button.remove();
      continue;
    }
    button.addEventListener('click', () => onAction(kind, deal));
  }
  panel.replaceChildren(node);
}

// Price history: a summary and the matched deals, so every match can be checked.
function renderHistory(section, history) {
  if (!history) return;
  section.hidden = false;
  section.querySelector('.history-line').textContent = historyLine(history);
  section.querySelector('summary').textContent = `Matched deals (${history.count})`;
  section.querySelector('ul').replaceChildren(...history.matches.map((m) => {
    const link = el('a', '', m.title);
    link.href = m.url;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    const li = el('li');
    li.append(el('span', 'price num', m.price), ' ',
      el('span', 'meta', [m.merchant, monthYear(m.first_seen)].filter(Boolean).join(' · ')), el('br'), link);
    return li;
  }));
  section.querySelector('.history-more').hidden = history.count <= history.matches.length;
}

// showSource prefixes a stale feed with its source label; only useful with several sources on.
export function renderStatus(node, feeds, { showSource = false } = {}) {
  const stale = feeds.filter((f) => f.stale);
  node.classList.toggle('bad', stale.length > 0);
  node.classList.toggle('ok', stale.length === 0);
  if (stale.length > 0) {
    node.textContent = stale
      .map((f) => `${showSource && f.label ? `${f.label} ` : ''}${f.feed} feed stale: last update ${ago(f.last_success)}${f.last_error ? ` (${f.last_error})` : ''}`)
      .join(' · ');
    return;
  }
  const latest = feeds.map((f) => f.last_success).filter(Boolean).sort().at(-1);
  node.textContent = latest ? `Feeds healthy · updated ${ago(latest)}` : 'Waiting for the first poll';
}

export function renderCounts(counts) {
  for (const node of document.querySelectorAll('[data-count]')) {
    const n = counts?.[node.dataset.count];
    node.textContent = n == null ? '' : String(n);
  }
}

export function renderError(panel, err) {
  panel.replaceChildren(el('li', 'empty error', `Something went wrong: ${err.message}`));
}

export function fillCategories(select, deals) {
  const current = select.value;
  const categories = [...new Set(deals.map((d) => d.category).filter(Boolean))]
    .sort((a, b) => a.localeCompare(b, 'de'));
  select.replaceChildren(new Option('All categories', ''), ...categories.map((c) => new Option(c, c)));
  select.value = categories.includes(current) ? current : '';
}

function fillSource(node, deal, show) {
  const text = show ? deal.source_label || deal.source || '' : '';
  node.textContent = text;
  node.hidden = !text;
}

function cssSafe(id) {
  return id.replace(/[^a-zA-Z0-9_-]/g, '-');
}

function el(tag, className = '', text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
