import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ago, age, clickKind, clock, heat, money, monthYear, ratingMark, sentence } from './format.js';

const now = Date.parse('2026-11-03T12:00:00Z');

test('never for missing times', () => assert.equal(ago(null, now), 'never'));
test('just now under a minute', () => assert.equal(ago('2026-11-03T11:59:40Z', now), 'just now'));
test('minutes', () => assert.equal(ago('2026-11-03T11:45:00Z', now), '15 min ago'));
test('hours past two hours', () => assert.equal(ago('2026-11-03T07:00:00Z', now), '5 h ago'));
test('ago ignores unparsable times', () => assert.equal(ago('not a date', now), 'never'));

test('age is compact', () => {
  assert.equal(age('2026-11-03T11:59:40Z', now), 'now');
  assert.equal(age('2026-11-03T11:25:00Z', now), '35 min');
  assert.equal(age('2026-11-03T07:00:00Z', now), '5 h');
  assert.equal(age('2026-10-31T12:00:00Z', now), '3 d');
  assert.equal(age(null, now), '');
});

test('clock shows local hours and minutes', () => {
  const d = new Date(2026, 10, 3, 9, 5);
  assert.equal(clock(d.toISOString()), '09:05');
  assert.equal(clock(null), '');
});

test('heat buckets temperatures', () => {
  assert.equal(heat(null), '');
  assert.equal(heat(-15), 'cold');
  assert.equal(heat(149), 'cold');
  assert.equal(heat(150), 'mild');
  assert.equal(heat(300), 'warm');
  assert.equal(heat(500), 'hot');
});

test('sentence capitalises the first letter only', () => {
  assert.equal(sentence('hot: 637° (minimum 100°)'), 'Hot: 637° (minimum 100°)');
  assert.equal(sentence(''), '');
});

test('ratingMark', () => {
  assert.deepEqual(ratingMark('good'), { symbol: '✓', label: 'good', className: 'good' });
  assert.deepEqual(ratingMark('meh'), { symbol: '~', label: 'meh', className: 'meh' });
  assert.deepEqual(ratingMark('bad'), { symbol: '!', label: 'bad', className: 'bad' });
  assert.equal(ratingMark('great'), null);
  assert.equal(ratingMark('toString'), null);
  assert.equal(ratingMark(undefined), null);
});

test('money', () => {
  assert.equal(money(4000, 'EUR', 'de-DE'), '40 €');
  assert.equal(money(129999, 'EUR', 'de-DE'), '1.299,99 €');
  assert.equal(money(1250, 'GBP', 'en-GB'), '£12.50');
});

test('monthYear', () => {
  assert.equal(monthYear('2026-08-14T09:12:00Z', 'en-GB'), 'Aug 2026');
  assert.equal(monthYear('', 'en-GB'), '');
});

test('clickKind', () => {
  assert.equal(clickKind('hide_deal'), 'Hidden');
  assert.equal(clickKind('junk'), 'Marked as junk');
  assert.equal(clickKind('other'), 'other');
});
