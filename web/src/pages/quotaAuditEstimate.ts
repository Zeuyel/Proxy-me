import type { QuotaAuditRow } from '../types/quotaAudit.ts';

const WEEKLY_WINDOW_SECONDS = 7 * 24 * 60 * 60;
const WEEKLY_WINDOW_TOLERANCE_SECONDS = 24 * 60 * 60;
const RESET_AT_DRIFT_TOLERANCE_MS = 5 * 60 * 1000;

interface QuotaEstimateSegment {
  window: string;
  durationSeconds: number | null;
  rows: QuotaAuditRow[];
  endTimestamp: number;
}

const timestampValue = (value: string | null | undefined) => {
  if (!value) return null;
  const timestamp = new Date(value).getTime();
  return Number.isFinite(timestamp) ? timestamp : null;
};

const isResetBoundary = (previous: QuotaAuditRow, current: QuotaAuditRow) => {
  if (current.reset || String(current.status || '').toLowerCase() === 'reset') return true;
  if (
    previous.window_duration_seconds != null &&
    current.window_duration_seconds != null &&
    previous.window_duration_seconds !== current.window_duration_seconds
  ) {
    return true;
  }

  const previousResetAt = timestampValue(previous.reset_at);
  const currentResetAt = timestampValue(current.reset_at);
  if (
    previousResetAt != null &&
    currentResetAt != null &&
    Math.abs(currentResetAt - previousResetAt) > RESET_AT_DRIFT_TOLERANCE_MS
  ) {
    return true;
  }

  const previousUsed = previous.used_percent;
  const currentUsed = current.used_percent;
  return previousUsed != null && currentUsed != null && currentUsed < previousUsed;
};

const hasUsageTokens = (row: QuotaAuditRow) =>
  row.tokens.input > 0 ||
  row.tokens.output > 0 ||
  row.tokens.reasoning > 0 ||
  row.tokens.cached > 0 ||
  row.tokens.total > 0;

const estimateSegmentCost = (
  segmentRows: QuotaAuditRow[],
  allRows: QuotaAuditRow[]
): number | null => {
  const usedRows = segmentRows
    .map((row, index) => ({ row, index }))
    .filter(({ row }) => row.used_percent != null && Number.isFinite(row.used_percent));
  if (usedRows.length < 2) return null;

  const first = usedRows[0];
  const last = usedRows[usedRows.length - 1];
  const quotaDelta = (last.row.used_percent as number) - (first.row.used_percent as number);
  if (!Number.isFinite(quotaDelta) || quotaDelta <= 0) return null;

  const intervalRows = segmentRows.slice(first.index + 1, last.index + 1);
  if (
    intervalRows.length === 0 ||
    intervalRows.some(
      (row) =>
        row.quota_delta_percent == null ||
        !Number.isFinite(row.quota_delta_percent) ||
        row.quota_delta_percent < 0
    )
  ) {
    return null;
  }

  const firstTimestamp = new Date(first.row.timestamp).getTime();
  const lastTimestamp = new Date(last.row.timestamp).getTime();
  const costRows = allRows.filter((row) => {
    const timestamp = new Date(row.timestamp).getTime();
    return timestamp > firstTimestamp && timestamp <= lastTimestamp;
  });
  if (
    costRows.some(
      (row) => hasUsageTokens(row) && (row.cost_delta_usd == null || !Number.isFinite(row.cost_delta_usd))
    )
  ) {
    return null;
  }
  const pricedRows = costRows.filter(
    (row) => row.cost_delta_usd != null && Number.isFinite(row.cost_delta_usd)
  );
  if (pricedRows.length === 0) return null;

  const cost = pricedRows.reduce((sum, row) => sum + (row.cost_delta_usd as number), 0);
  if (!Number.isFinite(cost) || cost < 0) return null;
  return (cost / quotaDelta) * 100;
};

const buildEstimateSegments = (rows: QuotaAuditRow[]): QuotaEstimateSegment[] => {
  const grouped = new Map<string, QuotaAuditRow[]>();
  for (const row of rows) {
    const group = grouped.get(row.window) || [];
    group.push(row);
    grouped.set(row.window, group);
  }

  const segments: QuotaEstimateSegment[] = [];
  for (const [window, windowRows] of grouped) {
    const sorted = [...windowRows].sort(
      (left, right) => new Date(left.timestamp).getTime() - new Date(right.timestamp).getTime()
    );
    let current: QuotaAuditRow[] = [];
    for (const row of sorted) {
      const previous = current[current.length - 1];
      if (previous && isResetBoundary(previous, row)) {
        segments.push(toEstimateSegment(window, current));
        current = [];
      }
      current.push(row);
    }
    if (current.length > 0) segments.push(toEstimateSegment(window, current));
  }
  return segments;
};

const toEstimateSegment = (window: string, rows: QuotaAuditRow[]): QuotaEstimateSegment => ({
  window,
  durationSeconds:
    rows.find((row) => row.window_duration_seconds != null)?.window_duration_seconds ?? null,
  rows,
  endTimestamp: new Date(rows[rows.length - 1].timestamp).getTime()
});

const isWeeklySegment = (segment: QuotaEstimateSegment) => {
  if (segment.durationSeconds != null) {
    return (
      Math.abs(segment.durationSeconds - WEEKLY_WINDOW_SECONDS) <=
      WEEKLY_WINDOW_TOLERANCE_SECONDS
    );
  }
  return /weekly|week|7d/i.test(segment.window);
};

export const buildEstimatedWeeklyLimit = (
  rows: QuotaAuditRow[],
  windowFilter: string
): number | null => {
  const candidates = buildEstimateSegments(rows).filter(
    (segment) => (!windowFilter || segment.window === windowFilter) && isWeeklySegment(segment)
  );
  if (candidates.length === 0) return null;

  const latest = [...candidates].sort((left, right) => right.endTimestamp - left.endTimestamp)[0];
  return estimateSegmentCost(latest.rows, rows);
};
