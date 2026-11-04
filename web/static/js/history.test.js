import { test } from 'node:test';
import assert from 'node:assert/strict';
import { historyChip, historyLine } from './history.js';

const lowest = { id: 'mydealz:2', url: 'https://example.test/2', title: 'Galaxy S24', merchant: 'Example Air', price: '549€', first_seen: '2026-08-14T09:12:00Z' };
const above = { status: 'above', currency: 'EUR', diff_cents: 4000, count: 3, lowest, matches: [lowest] };
const best = { status: 'best', currency: 'EUR', diff_cents: 0, count: 1, lowest, matches: [lowest] };

test('historyChip', () => {
  assert.equal(historyChip(undefined), null);
  assert.deepEqual(historyChip(best, 'de-DE'), { text: 'Best price', cls: 'best' });
  assert.deepEqual(historyChip(above, 'de-DE'), { text: '+40 €', cls: 'above' });
});

test('historyLine', () => {
  assert.equal(historyLine(undefined), '');
  assert.equal(historyLine(best, 'en-GB'), 'Best price in 180 days · 1 other deal');
  assert.equal(historyLine({ ...best, count: 4 }, 'en-GB'), 'Best price in 180 days · 4 other deals');
  assert.equal(historyLine(above, 'en-GB'), 'Lowest seen: 549€ at Example Air, Aug 2026 · now €40 more');
  assert.equal(historyLine({ ...above, lowest: { ...lowest, merchant: '' } }, 'en-GB'), 'Lowest seen: 549€, Aug 2026 · now €40 more');
});
