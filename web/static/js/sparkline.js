// Points for a <polyline> in a 100×24 viewBox, oldest value on the left.
export function sparkPoints(values, width = 100, height = 24) {
  if (!values || values.length < 2) return '';
  const min = Math.min(...values);
  const span = Math.max(...values) - min || 1;
  const step = width / (values.length - 1);
  return values
    .map((v, i) => `${round(i * step)},${round(height - ((v - min) / span) * height)}`)
    .join(' ');
}

const round = (n) => Math.round(n * 10) / 10;
