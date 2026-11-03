async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = res.statusText;
    try { message = (await res.json()).error || message; } catch { /* not JSON */ }
    throw new Error(message);
  }
  return res.status === 204 ? null : res.json();
}

export const getDeals = (view) => request('GET', `/api/deals?view=${encodeURIComponent(view)}`).then((r) => r.deals);
export const addAction = (kind, dealId) => request('POST', '/api/actions', { kind, deal_id: dealId });
export const deleteAction = (id) => request('DELETE', `/api/actions/${encodeURIComponent(id)}`);
export const getRules = () => request('GET', '/api/rules');
export const getStatus = () => request('GET', '/api/status').then((r) => r.feeds);
