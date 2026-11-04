// "15 min ago", for the feed status line.
export function ago(iso, now = Date.now()) {
  const minutes = minutesSince(iso, now);
  if (minutes === null) return 'never';
  if (minutes < 1) return 'just now';
  if (minutes < 120) return `${minutes} min ago`;
  return `${Math.round(minutes / 60)} h ago`;
}

// "35 min" / "5 h" / "3 d", for the narrow age column in the list.
export function age(iso, now = Date.now()) {
  const minutes = minutesSince(iso, now);
  if (minutes === null) return '';
  if (minutes < 1) return 'now';
  if (minutes < 60) return `${minutes} min`;
  if (minutes < 48 * 60) return `${Math.round(minutes / 60)} h`;
  return `${Math.round(minutes / 1440)} d`;
}

// Local "HH:MM".
export function clock(iso) {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return '';
  const d = new Date(t);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

// Colour bucket for a temperature; '' when unknown.
export function heat(temperature) {
  if (temperature == null) return '';
  if (temperature >= 500) return 'hot';
  if (temperature >= 300) return 'warm';
  if (temperature >= 150) return 'mild';
  return 'cold';
}

// Reasons are stored lowercase ("hot: 637°…"); the page shows them as sentences.
export function sentence(text) {
  return text ? text[0].toUpperCase() + text.slice(1) : '';
}

const MARKS = { good: '✓', meh: '~', bad: '!' };

// The judge's rating as a small marker; null when there is none.
export function ratingMark(rating) {
  if (!Object.hasOwn(MARKS, rating)) return null;
  return { symbol: MARKS[rating], label: rating, className: rating };
}

function minutesSince(iso, now) {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return null;
  return Math.round((now - t) / 60000);
}

// "40 €" / "£12.50": whole amounts without decimals.
export function money(cents, currency, locale) {
  const digits = cents % 100 === 0 ? 0 : 2;
  return new Intl.NumberFormat(locale, {
    style: 'currency', currency, minimumFractionDigits: digits, maximumFractionDigits: digits,
  }).format(cents / 100);
}

// "Aug 2026". UTC, so the month doesn't depend on the viewer's zone at a month's edge.
export function monthYear(iso, locale) {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return '';
  return new Date(t).toLocaleDateString(locale, { month: 'short', year: 'numeric', timeZone: 'UTC' });
}
