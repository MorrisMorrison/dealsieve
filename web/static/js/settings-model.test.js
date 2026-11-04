import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addTo, changeCount, clone, nextChannelName, removeFrom, TIMEZONES, withMinTemperature } from './settings-model.js';

const base = {
  watchlist: ['oled'],
  rules: { min_temperature: 100, category_overrides: {}, muted_categories: [], muted_merchants: [], block_keywords: [], recurrence: { window_days: 90, max_seen: 2 } },
  channels: {}, notify: [], suggestions: { min_count: 3, window_days: 30 },
};

test('clone is deep', () => {
  const c = clone(base);
  c.rules.recurrence.max_seen = 9;
  assert.equal(base.rules.recurrence.max_seen, 2);
});

test('addTo ignores blanks and case-insensitive duplicates', () => {
  assert.deepEqual(addTo(['oled'], '  Lego '), ['oled', 'Lego']);
  assert.deepEqual(addTo(['oled'], 'OLED'), ['oled']);
  assert.deepEqual(addTo(['oled'], '   '), ['oled']);
});

test('removeFrom matches exactly', () => {
  assert.deepEqual(removeFrom(['oled', 'lego'], 'oled'), ['lego']);
});

test('changeCount counts changed top-level sections and list entries', () => {
  const d = clone(base);
  assert.equal(changeCount(base, d), 0);
  d.watchlist = addTo(d.watchlist, 'lego');
  d.rules.min_temperature = 80;
  assert.equal(changeCount(base, d), 2);
  d.watchlist = removeFrom(d.watchlist, 'oled');
  assert.equal(changeCount(base, d), 3);
});

test('changeCount ignores case-only edits of list entries, like the server summary', () => {
  const d = clone(base);
  d.watchlist = ['OLED'];
  assert.equal(changeCount(base, d), 0);
  d.rules.block_keywords = ['Foo'];
  assert.equal(changeCount(base, d), 1);
});

test('nextChannelName picks a free lowercase name', () => {
  assert.equal(nextChannelName({}, 'ntfy'), 'ntfy');
  assert.equal(nextChannelName({ ntfy: {}, 'ntfy-2': {} }, 'ntfy'), 'ntfy-3');
});

test('changeCount counts a digest change once', () => {
  const saved = {
    ...base,
    digest: { enabled: false, time: '18:00', timezone: 'Europe/Berlin', top: 10, channels: [] },
  };
  const draft = clone(saved);
  draft.digest.enabled = true;
  draft.digest.time = '07:30';
  assert.equal(changeCount(saved, draft), 1);
});

test('withMinTemperature drops an empty field and parses a number', () => {
  assert.deepEqual(withMinTemperature({ enabled: true, min_temperature: 80 }, ''), { enabled: true });
  assert.deepEqual(withMinTemperature({ enabled: true }, '  '), { enabled: true });
  assert.deepEqual(withMinTemperature({ enabled: false }, '150'), { enabled: false, min_temperature: 150 });
});

test('changeCount counts a sources change once', () => {
  const saved = { ...base, sources: { mydealz: { enabled: true }, dealabs: { enabled: false } } };
  const draft = clone(saved);
  draft.sources.dealabs = { enabled: true, min_temperature: 150 };
  assert.equal(changeCount(saved, draft), 1);
});

test('TIMEZONES offers common zones', () => {
  assert.ok(TIMEZONES.includes('Europe/Berlin'));
  assert.ok(TIMEZONES.includes('UTC'));
});

test('changeCount counts a judge change once', () => {
  const saved = { ...base, judge: { enabled: false, model: '', daily_limit: 50, instructions: '' } };
  const draft = clone(saved);
  draft.judge.enabled = true;
  draft.judge.model = 'example/model';
  assert.equal(changeCount(saved, draft), 1);
});
