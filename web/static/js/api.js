async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = res.statusText;
    let parsed = null;
    try { parsed = await res.json(); message = parsed.error || message; } catch { /* not JSON */ }
    throw Object.assign(new Error(message), { status: res.status, body: parsed });
  }
  return res.status === 204 ? null : res.json();
}

// Resolves to { deals, counts } where counts has passed/watchlist/blocked.
export const getDeals = (view) => request('GET', `/api/deals?view=${encodeURIComponent(view)}`);
export const addAction = (kind, dealId) => request('POST', '/api/actions', { kind, deal_id: dealId });
export const undoAction = (kind, dealId) => request('POST', '/api/actions/undo', { kind, deal_id: dealId });
export const getStatus = () => request('GET', '/api/status').then((r) => r.feeds);

export const getSettings = () => request('GET', '/api/settings');
export const putSettings = (version, settings) => request('PUT', '/api/settings', { version, settings });
export const undoSettings = (version) => request('POST', '/api/settings/undo', { version });
export const getHistory = () => request('GET', '/api/settings/history').then((r) => r.history);
export const testChannel = (channel) => request('POST', '/api/settings/test', { channel });
export const testDigest = () => request('POST', '/api/settings/digest/test').then((r) => r.results);
export const getSuggestions = () => request('GET', '/api/suggestions').then((r) => r.suggestions);
export const applySuggestion = (kind, value) => request('POST', '/api/suggestions/apply', { kind, value }).then((r) => r.suggestions);
export const dismissSuggestion = (kind, value) => request('POST', '/api/suggestions/dismiss', { kind, value }).then((r) => r.suggestions);
export const getCategories = () => request('GET', '/api/categories').then((r) => r.categories);
