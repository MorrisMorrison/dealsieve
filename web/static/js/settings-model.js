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
  // The server compares entries case-insensitively when it summarises a save.
  const has = (list, x) => list.some((y) => y.toLowerCase() === x.toLowerCase());
  for (const [a, b] of lists) {
    n += b.filter((x) => !has(a, x)).length + a.filter((x) => !has(b, x)).length;
  }
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  if (saved.rules.min_temperature !== draft.rules.min_temperature) n++;
  if (!same(saved.rules.category_overrides, draft.rules.category_overrides)) n++;
  if (!same(saved.rules.recurrence, draft.rules.recurrence)) n++;
  if (!same(saved.channels, draft.channels)) n++;
  if (!same(saved.notify, draft.notify)) n++;
  if (!same(saved.suggestions, draft.suggestions)) n++;
  if (!same(saved.digest, draft.digest)) n++;
  if (!same(saved.judge, draft.judge)) n++;
  return n;
}

export function nextChannelName(channels, base) {
  if (!(base in channels)) return base;
  let i = 2;
  while (`${base}-${i}` in channels) i++;
  return `${base}-${i}`;
}

// Offered in the digest's time zone field; any IANA zone can be typed.
export const TIMEZONES = [
  'Europe/Berlin', 'Europe/Vienna', 'Europe/Zurich', 'Europe/Paris', 'Europe/Madrid', 'Europe/Rome',
  'Europe/Amsterdam', 'Europe/Brussels', 'Europe/London', 'Europe/Dublin', 'Europe/Lisbon', 'Europe/Warsaw',
  'America/New_York', 'America/Chicago', 'America/Los_Angeles', 'Asia/Tokyo', 'Australia/Sydney', 'UTC',
];
