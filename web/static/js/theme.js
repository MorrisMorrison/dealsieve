const KEY = 'dealsieve-theme';

export function initTheme(button) {
  const saved = read();
  if (saved) document.documentElement.dataset.theme = saved;
  button.addEventListener('click', () => {
    const current = document.documentElement.dataset.theme
      || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    const next = current === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    write(next);
  });
}

// Storage can be blocked (private windows); the toggle still works per visit.
function read() {
  try { return localStorage.getItem(KEY); } catch { return null; }
}

function write(value) {
  try { localStorage.setItem(KEY, value); } catch { /* ignore */ }
}
