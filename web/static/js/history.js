import { money, monthYear } from './format.js';

// The list chip for a deal's price history, or null without one.
export function historyChip(h, locale) {
  if (!h) return null;
  if (h.status === 'best') return { text: 'Best price', cls: 'best' };
  return { text: `+${money(h.diff_cents, h.currency, locale)}`, cls: 'above' };
}

// The detail panel's summary line.
export function historyLine(h, locale) {
  if (!h) return '';
  if (h.status === 'best') {
    return `Best price in 180 days · ${h.count === 1 ? 'seen once before' : `seen ${h.count}× before`}`;
  }
  const where = h.lowest.merchant ? ` at ${h.lowest.merchant}` : '';
  return `Lowest seen: ${h.lowest.price}${where}, ${monthYear(h.lowest.first_seen, locale)} · ${money(h.diff_cents, h.currency, locale)} more than now`;
}
