export function ago(iso, now = Date.now()) {
  if (!iso) return 'never';
  const minutes = Math.round((now - Date.parse(iso)) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 120) return `${minutes} min ago`;
  return `${Math.round(minutes / 60)} h ago`;
}
