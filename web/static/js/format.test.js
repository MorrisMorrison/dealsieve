import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ago } from './format.js';

const now = Date.parse('2026-11-03T12:00:00Z');

test('never for missing times', () => assert.equal(ago(null, now), 'never'));
test('just now under a minute', () => assert.equal(ago('2026-11-03T11:59:40Z', now), 'just now'));
test('minutes', () => assert.equal(ago('2026-11-03T11:45:00Z', now), '15 min ago'));
test('hours past two hours', () => assert.equal(ago('2026-11-03T07:00:00Z', now), '5 h ago'));
