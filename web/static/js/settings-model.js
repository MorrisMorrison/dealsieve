// Pure helpers for the settings page: no DOM, unit-tested with node --test.

export const clone = (doc) => JSON.parse(JSON.stringify(doc));

export function addTo(list, value) {
  const v = String(value).trim();
  if (!v || list.some((x) => x.toLowerCase() === v.toLowerCase())) return list;
  return [...list, v];
}

export const removeFrom = (list, value) => list.filter((x) => x !== value);

// Counts what the save bar reports: each added or removed list entry, and
// each other changed setting.
export function changeCount(saved, draft) {
  let n = 0;
  const lists = [
    [saved.watchlist, draft.watchlist],
    [saved.rules.muted_merchants, draft.rules.muted_merchants],
    [saved.rules.muted_categories, draft.rules.muted_categories],
    [saved.rules.block_keywords, draft.rules.block_keywords],
  ];
  for (const [a, b] of lists) {
    n += b.filter((x) => !a.includes(x)).length + a.filter((x) => !b.includes(x)).length;
  }
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  if (saved.rules.min_temperature !== draft.rules.min_temperature) n++;
  if (!same(saved.rules.category_overrides, draft.rules.category_overrides)) n++;
  if (!same(saved.rules.recurrence, draft.rules.recurrence)) n++;
  if (!same(saved.channels, draft.channels)) n++;
  if (!same(saved.notify, draft.notify)) n++;
  if (!same(saved.suggestions, draft.suggestions)) n++;
  return n;
}

export function nextChannelName(channels, base) {
  if (!(base in channels)) return base;
  let i = 2;
  while (`${base}-${i}` in channels) i++;
  return `${base}-${i}`;
}
