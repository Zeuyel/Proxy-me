import assert from 'node:assert/strict';
import test from 'node:test';
import { buildEstimatedWeeklyLimit } from '../src/pages/quotaAuditEstimate.ts';

const WEEK = 604800;
const tokens = (total = 0) => ({
  input: total,
  output: 0,
  reasoning: 0,
  cached: 0,
  total
});

const row = ({
  timestamp,
  used,
  delta = null,
  cost = null,
  duration = WEEK,
  window = 'weekly',
  resetAt = '2026-08-24T00:00:00Z',
  reset = false,
  tokenTotal = cost == null ? 0 : 1
}) => ({
  auth: 'account-1',
  window,
  window_duration_seconds: duration,
  timestamp,
  used_percent: used,
  quota_delta_percent: delta,
  cost_delta_usd: cost,
  reset_at: resetAt,
  reset,
  tokens: tokens(tokenTotal)
});

test('extrapolates the current weekly segment from first and last snapshots', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20 }),
    row({ timestamp: '2026-08-20T01:00:00Z', used: 40, delta: 20, cost: 100 })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, ''), 500);
});

test('starts over after reset and returns null with only one new snapshot', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20 }),
    row({ timestamp: '2026-08-20T01:00:00Z', used: 40, delta: 20, cost: 100 }),
    row({
      timestamp: '2026-08-21T00:00:00Z',
      used: 5,
      reset: true,
      resetAt: '2026-08-28T00:00:00Z'
    })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, ''), null);
});

test('treats a significant reset_at change as a new segment even when used increases', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20 }),
    row({ timestamp: '2026-08-20T01:00:00Z', used: 30, delta: 10, cost: 50 }),
    row({
      timestamp: '2026-08-21T00:00:00Z',
      used: 35,
      delta: 5,
      cost: 20,
      resetAt: '2026-08-28T00:00:00Z'
    })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, ''), null);
});

test('tolerates small reset_at drift within the same segment', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20 }),
    row({
      timestamp: '2026-08-20T01:00:00Z',
      used: 30,
      delta: 10,
      cost: 50,
      resetAt: '2026-08-24T00:02:00Z'
    })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, ''), 500);
});

test('returns null when usage in the segment cannot be priced', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20 }),
    row({
      timestamp: '2026-08-20T01:00:00Z',
      used: 40,
      delta: 20,
      cost: null,
      tokenTotal: 100
    })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, ''), null);
});

test('returns null for a non-weekly window', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20, duration: 18000, window: 'primary' }),
    row({
      timestamp: '2026-08-20T01:00:00Z',
      used: 40,
      delta: 20,
      cost: 100,
      duration: 18000,
      window: 'primary'
    })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, 'primary'), null);
});

test('uses the uniquely assigned cost from another window in all-window responses', () => {
  const rows = [
    row({ timestamp: '2026-08-20T00:00:00Z', used: 20 }),
    row({ timestamp: '2026-08-20T01:00:00Z', used: 40, delta: 20 }),
    row({
      timestamp: '2026-08-20T00:00:00Z',
      used: 10,
      duration: 18000,
      window: 'primary'
    }),
    row({
      timestamp: '2026-08-20T01:00:00Z',
      used: 30,
      delta: 20,
      cost: 100,
      duration: 18000,
      window: 'primary'
    })
  ];
  assert.equal(buildEstimatedWeeklyLimit(rows, ''), 500);
});
