const KEY = 'dealsieve-theme';

export function initTheme(button) {
  const saved = read();
  if (saved) {
    document.documentElement.dataset.theme = saved;
    syncThemeColor(saved);
  }
  button.addEventListener('click', () => {
    const current = document.documentElement.dataset.theme
      || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    const next = current === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    syncThemeColor(next);
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

// The theme-color metas follow the OS setting through their media queries; once the
// user picks a theme by hand, one unconditional value must win so the bar matches the page.
const BAR = { light: '#f7f7f8', dark: '#0f0f11' };

function syncThemeColor(theme) {
  const metas = document.querySelectorAll('meta[name="theme-color"]');
  metas.forEach((m, i) => {
    if (i === 0) { m.removeAttribute('media'); m.content = BAR[theme]; } else m.remove();
  });
}
