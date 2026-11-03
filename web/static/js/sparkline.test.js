import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sparkPoints } from './sparkline.js';

test('needs at least two values', () => {
  assert.equal(sparkPoints([]), '');
  assert.equal(sparkPoints([42]), '');
});

test('scales values into the 100x24 box', () => {
  assert.equal(sparkPoints([0, 50, 100]), '0,24 50,12 100,0');
});

test('a flat series sits on the bottom', () => {
  assert.equal(sparkPoints([7, 7]), '0,24 100,24');
});
