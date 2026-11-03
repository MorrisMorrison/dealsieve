import { test } from 'node:test';
import assert from 'node:assert/strict';
import { areaPoints, sparkPoints } from './sparkline.js';

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

test('area closes the line down to the bottom edge', () => {
  assert.equal(areaPoints([0, 100], 100, 40), '0,40 0,40 100,0 100,40');
  assert.equal(areaPoints([5]), '');
});
