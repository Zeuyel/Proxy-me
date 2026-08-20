import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Chart as ChartJS,
  CategoryScale,
  Filler,
  Legend,
  LineElement,
  LinearScale,
  PointElement,
  Tooltip,
  type ChartData,
  type ChartOptions
} from 'chart.js';
import { Line } from 'react-chartjs-2';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { quotaAuditApi, type QuotaAuditQuery } from '@/services/api';
import type {
  QuotaAuditResponse,
  QuotaAuditAccount,
  QuotaAuditRow,
  QuotaAuditStatus,
  QuotaAuditSummary,
  QuotaAuditTokens
} from '@/types/quotaAudit';
import { useThemeStore } from '@/stores';
import styles from './QuotaAuditPage.module.scss';

ChartJS.register(CategoryScale, Filler, Legend, LineElement, LinearScale, PointElement, Tooltip);

interface AuditFilters {
  account: string;
  window: string;
  model: string;
  from: string;
  to: string;
}

type RowState = 'ok' | 'warning' | 'reset' | 'unknown' | 'negative' | 'stale';

interface TrendPoint {
  timestamp: string;
  usedPercent: number | null;
  quotaDeltaPercent: number | null;
  costDeltaUsd: number | null;
  totalTokens: number | null;
  reset: boolean;
}

const EMPTY_FILTERS: AuditFilters = {
  account: '',
  window: '',
  model: '',
  from: '',
  to: ''
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;

const firstValue = (record: Record<string, unknown>, keys: string[]): unknown => {
  for (const key of keys) {
    if (record[key] !== undefined && record[key] !== null) return record[key];
  }
  return undefined;
};

const toNumber = (value: unknown): number | null => {
  if (typeof value === 'number') return Number.isFinite(value) ? value : null;
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
};

const toText = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const toBoolean = (value: unknown): boolean =>
  value === true || value === 1 || value === '1' || value === 'true';

const toStringArray = (value: unknown): string[] => {
  if (!Array.isArray(value)) return [];
  return value.map(toText).filter(Boolean);
};

const readNumber = (record: Record<string, unknown>, keys: string[], fallback: number | null = null) =>
  toNumber(firstValue(record, keys)) ?? fallback;

const normalizeTokens = (value: unknown): QuotaAuditTokens => {
  const source = isRecord(value) ? value : {};
  return {
    input: readNumber(source, ['input', 'input_tokens', 'prompt_tokens'], 0) ?? 0,
    output: readNumber(source, ['output', 'output_tokens', 'completion_tokens'], 0) ?? 0,
    reasoning: readNumber(source, ['reasoning', 'reasoning_tokens'], 0) ?? 0,
    cached: readNumber(source, ['cached', 'cached_tokens', 'cache_read_input_tokens'], 0) ?? 0,
    total: readNumber(source, ['total', 'total_tokens'], 0) ?? 0
  };
};

const normalizeRow = (value: unknown): QuotaAuditRow | null => {
  if (!isRecord(value)) return null;
  const authIndex = toText(firstValue(value, ['auth_index', 'authIndex', 'index'])) || undefined;
  const authId = toText(firstValue(value, ['auth_id', 'authId'])) || undefined;
  const auth = authIndex || toText(firstValue(value, ['auth', 'auth_id', 'account'])) || 'unknown';
  const account = toText(firstValue(value, ['account', 'email', 'auth_name'])) || undefined;
  const timestamp = toText(firstValue(value, ['timestamp', 'time', 'at', 'observed_at']));
  const window = toText(firstValue(value, ['window', 'quota_window', 'window_name'])) || 'unknown';
  const snapshotId = toText(firstValue(value, ['snapshot_id', 'snapshotId', 'id'])) || undefined;
  const planType = toText(firstValue(value, ['plan_type', 'planType'])) || undefined;
  const model = toText(firstValue(value, ['model', 'model_name'])) || undefined;
  const tokenSource = firstValue(value, ['tokens', 'token_delta', 'token_usage', 'usage']) ?? value;
  const status = toText(value.status) as QuotaAuditStatus | undefined;
  const priceSnapshot = isRecord(value.price_snapshot) ? value.price_snapshot : null;

  if (!timestamp && auth === 'unknown') return null;

  return {
    snapshot_id: snapshotId,
    auth,
    auth_id: authId,
    auth_index: authIndex,
    account,
    window,
    plan_type: planType,
    model,
    session_ids: toStringArray(firstValue(value, ['session_ids', 'sessionIds', 'sessions'])),
    thread_ids: toStringArray(firstValue(value, ['thread_ids', 'threadIds', 'threads'])),
    timestamp,
    used_percent: readNumber(value, ['used_percent', 'quota_used_percent', 'used']),
    remaining_percent: readNumber(value, ['remaining_percent', 'remainingPercent', 'remaining']),
    quota_delta_percent: readNumber(value, ['quota_delta_percent', 'quota_delta', 'delta_percent']),
    tokens: normalizeTokens(tokenSource),
    cost_delta_usd: readNumber(value, ['cost_delta_usd', 'cost_delta', 'delta_cost_usd']),
    cost_per_quota_percent: readNumber(value, [
      'cost_per_quota_percent',
      'cost_per_quota',
      'unit_quota_cost_usd'
    ]),
    cost_status: toText(firstValue(value, ['cost_status', 'costStatus'])) || undefined,
    status,
    reset: toBoolean(value.reset),
    reset_at: toText(firstValue(value, ['reset_at', 'quota_reset_at'])) || null,
    stale: toBoolean(value.stale),
    reason: toText(value.reason) || null,
    price_snapshot: priceSnapshot
      ? {
          input_per_million_usd: readNumber(priceSnapshot, ['input_per_million_usd']),
          output_per_million_usd: readNumber(priceSnapshot, ['output_per_million_usd']),
          reasoning_per_million_usd: readNumber(priceSnapshot, ['reasoning_per_million_usd']),
          cached_per_million_usd: readNumber(priceSnapshot, ['cached_per_million_usd']),
          currency: toText(priceSnapshot.currency) || undefined,
          captured_at: toText(priceSnapshot.captured_at) || undefined,
          source: toText(priceSnapshot.source) || undefined,
          version: toText(priceSnapshot.version) || undefined,
          fingerprint: toText(priceSnapshot.fingerprint) || undefined,
          unit: toText(priceSnapshot.unit) || undefined,
          immutable: toBoolean(priceSnapshot.immutable)
        }
      : null
  };
};

const normalizeAccount = (value: unknown): QuotaAuditAccount | null => {
  if (!isRecord(value)) return null;
  const authId = toText(firstValue(value, ['auth_id', 'authId', 'auth', 'id']));
  const authIndex = toText(firstValue(value, ['auth_index', 'authIndex', 'index'])) || undefined;
  const account = toText(firstValue(value, ['account', 'email', 'auth_name'])) || undefined;
  if (!authId && !authIndex && !account) return null;
  return {
    auth_id: authId || authIndex || account || 'unknown',
    auth_index: authIndex,
    account,
    provider: toText(firstValue(value, ['provider', 'type'])) || undefined,
    disabled: toBoolean(value.disabled),
    updated_at: toText(firstValue(value, ['updated_at', 'updatedAt', 'timestamp'])) || undefined
  };
};

const normalizeResponse = (value: unknown): QuotaAuditResponse => {
  const source = isRecord(value) ? value : {};
  const nested = isRecord(source.data) ? source.data : source;
  const rawRows = Array.isArray(nested.rows)
    ? nested.rows
    : Array.isArray(nested.snapshots)
      ? nested.snapshots
      : [];
  const rows = rawRows.map(normalizeRow).filter((row): row is QuotaAuditRow => row !== null);
  const accounts = Array.isArray(nested.accounts)
    ? nested.accounts.map(normalizeAccount).filter((account): account is QuotaAuditAccount => account !== null)
    : [];
  return {
    rows,
    snapshots: rows,
    accounts,
    summary: isRecord(nested.summary) ? (nested.summary as QuotaAuditSummary) : undefined,
    price_snapshot: isRecord(nested.price_snapshot) ? nested.price_snapshot : null
  };
};

const rowIdentity = (row: QuotaAuditRow) => row.auth_index || row.auth || row.auth_id || row.account || 'unknown';

const accountIdentity = (account: QuotaAuditAccount) =>
  account.auth_index || account.auth_id || account.account || 'unknown';

const accountLabel = (account: QuotaAuditAccount) => {
  const label = account.account || 'unknown';
  const suffix = (account.auth_index || account.auth_id).slice(0, 8);
  return suffix ? `${label} · ${suffix}` : label;
};

const rowAccount = (row: QuotaAuditRow, labels?: Map<string, string>) =>
  labels?.get(rowIdentity(row)) || row.account || row.auth || 'unknown';

const uniqueValues = (values: string[]) =>
  Array.from(new Set(values.map((value) => value.trim()).filter(Boolean))).sort((a, b) =>
    a.localeCompare(b)
  );

const toQuery = (filters: AuditFilters): QuotaAuditQuery => {
  const query: QuotaAuditQuery = {
    auth_index: filters.account || undefined,
    window: filters.window || undefined,
    model: filters.model || undefined
  };
  if (filters.from) query.from = new Date(filters.from).toISOString();
  if (filters.to) query.to = new Date(filters.to).toISOString();
  return query;
};

const matchesFilters = (row: QuotaAuditRow, filters: AuditFilters) => {
  if (filters.account && rowIdentity(row) !== filters.account) return false;
  if (filters.window && row.window !== filters.window) return false;
  if (filters.model && (row.model || 'unknown') !== filters.model) return false;
  const timestamp = new Date(row.timestamp).getTime();
  if (filters.from && Number.isFinite(timestamp) && timestamp < new Date(filters.from).getTime()) return false;
  if (filters.to && Number.isFinite(timestamp) && timestamp > new Date(filters.to).getTime()) return false;
  return true;
};

const filterRows = (rows: QuotaAuditRow[], filters: AuditFilters) =>
  rows.filter((row) => matchesFilters(row, filters));

const sumNullable = (values: Array<number | null | undefined>) => {
  const present = values.filter((value): value is number => typeof value === 'number' && Number.isFinite(value));
  return present.length ? present.reduce((sum, value) => sum + value, 0) : null;
};

const averageNullable = (values: Array<number | null | undefined>) => {
  const present = values.filter((value): value is number => typeof value === 'number' && Number.isFinite(value));
  return present.length ? present.reduce((sum, value) => sum + value, 0) / present.length : null;
};

const getRowState = (row: QuotaAuditRow): RowState => {
  const status = String(row.status || '').toLowerCase();
  if (row.reset || status === 'reset') return 'reset';
  if (row.stale || status === 'stale') return 'stale';
  if (status === 'negative' || (row.quota_delta_percent !== null && row.quota_delta_percent !== undefined && row.quota_delta_percent < 0)) {
    return 'negative';
  }
  if (status === 'unknown' || (row.used_percent == null && row.quota_delta_percent == null)) return 'unknown';
  if (status === 'warning') return 'warning';
  return 'ok';
};

const buildTrend = (rows: QuotaAuditRow[]): TrendPoint[] => {
  const groups = new Map<string, QuotaAuditRow[]>();
  rows.forEach((row) => {
    if (!Number.isFinite(new Date(row.timestamp).getTime())) return;
    const group = groups.get(row.timestamp) || [];
    group.push(row);
    groups.set(row.timestamp, group);
  });

  return Array.from(groups.entries())
    .sort(([left], [right]) => new Date(left).getTime() - new Date(right).getTime())
    .map(([timestamp, groupedRows]) => ({
      timestamp,
      usedPercent: averageNullable(groupedRows.map((row) => row.used_percent)),
      quotaDeltaPercent: sumNullable(groupedRows.map((row) => row.quota_delta_percent)),
      costDeltaUsd: sumNullable(groupedRows.map((row) => row.cost_delta_usd)),
      totalTokens: sumNullable(groupedRows.map((row) => row.tokens.total)),
      reset: groupedRows.some((row) => getRowState(row) === 'reset')
    }));
};

const formatNumber = (value: number | null | undefined, maximumFractionDigits = 0) =>
  value == null || !Number.isFinite(value)
    ? '—'
    : value.toLocaleString(undefined, { maximumFractionDigits, minimumFractionDigits: maximumFractionDigits });

const formatPercent = (value: number | null | undefined) =>
  value == null || !Number.isFinite(value) ? '—' : `${value.toFixed(2)}%`;

const formatSignedPercent = (value: number | null | undefined) => {
  if (value == null || !Number.isFinite(value)) return '—';
  return `${value > 0 ? '+' : ''}${value.toFixed(2)}%`;
};

const formatCurrency = (value: number | null | undefined, digits = 4) => {
  if (value == null || !Number.isFinite(value)) return '—';
  return `${value >= 0 ? '+' : '-'}$${Math.abs(value).toFixed(digits)}`;
};

const formatTimestamp = (value: string, locale: string) => {
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString(locale) : value || '—';
};

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error && error.message) return error.message;
  return 'quota-audit endpoint unavailable';
};

const chartColors = {
  quota: '#3b82f6',
  delta: '#f97316',
  cost: '#10b981',
  tokens: '#8b5cf6'
};

interface ChartLabels {
  usedPercent: string;
  quotaDelta: string;
  costDelta: string;
  totalTokens: string;
}

const buildQuotaChartData = (
  trend: TrendPoint[],
  language: string,
  labels: ChartLabels
): ChartData<'line'> => ({
  labels: trend.map((point) => formatTimestamp(point.timestamp, language)),
  datasets: [
    {
      label: labels.usedPercent,
      data: trend.map((point) => point.usedPercent),
      borderColor: chartColors.quota,
      backgroundColor: `${chartColors.quota}22`,
      pointBackgroundColor: trend.map((point) => (point.reset ? chartColors.delta : chartColors.quota)),
      pointRadius: trend.map((point) => (point.reset ? 5 : 3)),
      tension: 0.25,
      fill: true,
      spanGaps: false,
      yAxisID: 'y'
    },
    {
      label: labels.quotaDelta,
      data: trend.map((point) => point.quotaDeltaPercent),
      borderColor: chartColors.delta,
      backgroundColor: 'transparent',
      pointBackgroundColor: chartColors.delta,
      pointRadius: 3,
      tension: 0.25,
      spanGaps: false,
      yAxisID: 'y'
    }
  ]
});

const buildCostTokenChartData = (
  trend: TrendPoint[],
  language: string,
  labels: ChartLabels
): ChartData<'line'> => ({
  labels: trend.map((point) => formatTimestamp(point.timestamp, language)),
  datasets: [
    {
      label: labels.costDelta,
      data: trend.map((point) => point.costDeltaUsd),
      borderColor: chartColors.cost,
      backgroundColor: `${chartColors.cost}22`,
      pointBackgroundColor: chartColors.cost,
      pointRadius: 3,
      tension: 0.25,
      fill: true,
      spanGaps: false,
      yAxisID: 'cost'
    },
    {
      label: labels.totalTokens,
      data: trend.map((point) => point.totalTokens),
      borderColor: chartColors.tokens,
      backgroundColor: 'transparent',
      pointBackgroundColor: chartColors.tokens,
      pointRadius: 3,
      tension: 0.25,
      spanGaps: false,
      yAxisID: 'tokens'
    }
  ]
});

export function QuotaAuditPage() {
  const { t, i18n } = useTranslation();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const isDark = resolvedTheme === 'dark';
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [appliedFilters, setAppliedFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [response, setResponse] = useState<QuotaAuditResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadAudit = useCallback(async (nextFilters: AuditFilters) => {
    setLoading(true);
    setError(null);
    try {
      const payload = await quotaAuditApi.getAudit(toQuery(nextFilters));
      setResponse(normalizeResponse(payload));
    } catch (requestError) {
      setResponse(null);
      setError(getErrorMessage(requestError));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAudit(EMPTY_FILTERS);
  }, [loadAudit]);

  const refreshCurrent = useCallback(() => loadAudit(appliedFilters), [appliedFilters, loadAudit]);
  useHeaderRefresh(refreshCurrent);

  const rows = useMemo(() => response?.rows || response?.snapshots || [], [response]);
  const serverAccounts = useMemo(() => response?.accounts || [], [response]);
  const visibleRows = useMemo(() => filterRows(rows, appliedFilters), [appliedFilters, rows]);
  const accountLabels = useMemo(
    () => new Map(serverAccounts.map((account) => [accountIdentity(account), accountLabel(account)])),
    [serverAccounts]
  );
  const accounts = useMemo(() => {
    const options = new Map(accountLabels);
    for (const row of rows) {
      const identity = rowIdentity(row);
      if (!options.has(identity)) options.set(identity, rowAccount(row));
    }
    return Array.from(options, ([value, label]) => ({ value, label })).sort((left, right) =>
      left.label.localeCompare(right.label)
    );
  }, [accountLabels, rows]);
  const windows = useMemo(() => uniqueValues(rows.map((row) => row.window || 'unknown')), [rows]);
  const models = useMemo(() => uniqueValues(rows.map((row) => row.model || 'unknown')), [rows]);
  const accountCharts = useMemo(() => {
    const grouped = new Map<string, QuotaAuditRow[]>();
    for (const row of visibleRows) {
      const identity = rowIdentity(row);
      const group = grouped.get(identity) || [];
      group.push(row);
      grouped.set(identity, group);
    }
    return Array.from(grouped.entries())
      .map(([identity, groupRows]) => ({
        identity,
        label: accountLabels.get(identity) || rowAccount(groupRows[0]),
        trend: buildTrend(groupRows)
      }))
      .filter((group) => group.trend.length > 0);
  }, [accountLabels, visibleRows]);

  const accountSummaries = useMemo(
    () =>
      accounts.map((account) => {
        const accountRows = rows.filter((row) => rowIdentity(row) === account.value);
        const latest = accountRows.reduce<QuotaAuditRow | null>((current, row) => {
          if (!current) return row;
          return new Date(row.timestamp).getTime() > new Date(current.timestamp).getTime() ? row : current;
        }, null);
        return {
          ...account,
          samples: accountRows.length,
          usedPercent: latest?.used_percent ?? null,
          disabled: serverAccounts.find((candidate) => accountIdentity(candidate) === account.value)?.disabled ?? false
        };
      }),
    [accounts, rows]
  );

  const summary = useMemo(() => {
    const source = response?.summary;
    const hasServerSummary = visibleRows.length === rows.length && Boolean(source);
    const staleSamples = visibleRows.filter((row) => getRowState(row) === 'stale').length;
    const resetSamples = visibleRows.filter((row) => getRowState(row) === 'reset').length;
    const rosterAccounts = new Set(serverAccounts.map(accountIdentity));
    const rowAccounts = new Set(visibleRows.map(rowIdentity));
    return {
      accounts: hasServerSummary && source?.accounts != null ? source.accounts : new Set([...rosterAccounts, ...rowAccounts]).size,
      windows: hasServerSummary && source?.windows != null ? source.windows : new Set(visibleRows.map((row) => row.window)).size,
      samples: hasServerSummary && source?.samples != null ? source.samples : visibleRows.length,
      usedPercent: hasServerSummary ? source?.used_percent ?? averageNullable(visibleRows.map((row) => row.used_percent)) : averageNullable(visibleRows.map((row) => row.used_percent)),
      quotaDeltaPercent: hasServerSummary ? source?.quota_delta_percent ?? sumNullable(visibleRows.map((row) => row.quota_delta_percent)) : sumNullable(visibleRows.map((row) => row.quota_delta_percent)),
      totalTokens: hasServerSummary ? source?.total_tokens ?? sumNullable(visibleRows.map((row) => row.tokens.total)) : sumNullable(visibleRows.map((row) => row.tokens.total)),
      costDeltaUsd: hasServerSummary ? source?.cost_delta_usd ?? sumNullable(visibleRows.map((row) => row.cost_delta_usd)) : sumNullable(visibleRows.map((row) => row.cost_delta_usd)),
      staleSamples: hasServerSummary ? source?.stale_samples ?? staleSamples : staleSamples,
      resetSamples: hasServerSummary ? source?.reset_samples ?? resetSamples : resetSamples
    };
  }, [response?.summary, rows.length, serverAccounts, visibleRows]);

  const priceSnapshot = response?.price_snapshot || visibleRows.find((row) => row.price_snapshot)?.price_snapshot;

  const chartLabels: ChartLabels = {
    usedPercent: t('quota_audit.chart_used_percent'),
    quotaDelta: t('quota_audit.chart_quota_delta'),
    costDelta: t('quota_audit.chart_cost_delta'),
    totalTokens: t('quota_audit.chart_total_tokens')
  };

  const chartOptions = useMemo<ChartOptions<'line'>>(
    () => ({
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: {
          labels: { color: isDark ? '#e5e7eb' : '#374151' }
        }
      },
      scales: {
        y: {
          beginAtZero: true,
          suggestedMax: 100,
          ticks: { color: isDark ? '#9ca3af' : '#6b7280' },
          grid: { color: isDark ? '#374151' : '#e5e7eb' },
          title: { display: true, text: t('quota_audit.percent_axis'), color: isDark ? '#9ca3af' : '#6b7280' }
        },
        cost: {
          beginAtZero: true,
          position: 'left',
          ticks: { color: isDark ? '#9ca3af' : '#6b7280' },
          grid: { color: isDark ? '#374151' : '#e5e7eb' },
          title: { display: true, text: t('quota_audit.cost_axis'), color: isDark ? '#9ca3af' : '#6b7280' }
        },
        tokens: {
          beginAtZero: true,
          position: 'right',
          grid: { drawOnChartArea: false },
          ticks: { color: isDark ? '#9ca3af' : '#6b7280' },
          title: { display: true, text: t('quota_audit.tokens_axis'), color: isDark ? '#9ca3af' : '#6b7280' }
        }
      }
    }),
    [isDark, t]
  );

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setAppliedFilters(filters);
    void loadAudit(filters);
  };

  const handleReset = () => {
    setFilters(EMPTY_FILTERS);
    setAppliedFilters(EMPTY_FILTERS);
    void loadAudit(EMPTY_FILTERS);
  };

  const handleSelectAccount = (account: string) => {
    const nextFilters: AuditFilters = { ...EMPTY_FILTERS, account };
    setFilters(nextFilters);
    setAppliedFilters(nextFilters);
    void loadAudit(nextFilters);
  };

  const handleBackToAccounts = () => {
    setFilters(EMPTY_FILTERS);
    setAppliedFilters(EMPTY_FILTERS);
    void loadAudit(EMPTY_FILTERS);
  };

  const statusLabel = (state: RowState) => t(`quota_audit.status_${state}`);

  return (
    <div className={styles.container}>
      {loading && (
        <div className={styles.loadingOverlay} aria-busy="true">
          <div className={styles.loadingOverlayContent}>
            <LoadingSpinner size={26} />
            <span>{t('common.loading')}</span>
          </div>
        </div>
      )}

      <div className={styles.header}>
        <div>
          <h1 className={styles.pageTitle}>{t('quota_audit.title')}</h1>
          <p className={styles.pageSubtitle}>{t('quota_audit.description')}</p>
        </div>
        <Button variant="secondary" size="sm" onClick={refreshCurrent} disabled={loading}>
          {t('quota_audit.refresh')}
        </Button>
      </div>

      <form className={styles.filters} onSubmit={handleSubmit}>
        <label className={styles.filterField}>
          <span>{t('quota_audit.account')}</span>
          <select
            value={filters.account}
            onChange={(event) => setFilters((current) => ({ ...current, account: event.target.value }))}
          >
            <option value="">{t('quota_audit.all_accounts')}</option>
            {accounts.map((account) => (
              <option key={account.value} value={account.value}>
                {account.label}
              </option>
            ))}
          </select>
        </label>
        <label className={styles.filterField}>
          <span>{t('quota_audit.window')}</span>
          <select
            value={filters.window}
            onChange={(event) => setFilters((current) => ({ ...current, window: event.target.value }))}
          >
            <option value="">{t('quota_audit.all_windows')}</option>
            {windows.map((window) => (
              <option key={window} value={window}>
                {window}
              </option>
            ))}
          </select>
        </label>
        <label className={styles.filterField}>
          <span>{t('quota_audit.model')}</span>
          <select
            value={filters.model}
            onChange={(event) => setFilters((current) => ({ ...current, model: event.target.value }))}
          >
            <option value="">{t('quota_audit.all_models')}</option>
            {models.map((model) => (
              <option key={model} value={model}>
                {model}
              </option>
            ))}
          </select>
        </label>
        <label className={styles.filterField}>
          <span>{t('quota_audit.from')}</span>
          <input
            type="datetime-local"
            value={filters.from}
            onChange={(event) => setFilters((current) => ({ ...current, from: event.target.value }))}
          />
        </label>
        <label className={styles.filterField}>
          <span>{t('quota_audit.to')}</span>
          <input
            type="datetime-local"
            value={filters.to}
            onChange={(event) => setFilters((current) => ({ ...current, to: event.target.value }))}
          />
        </label>
        <div className={styles.filterActions}>
          <Button type="submit" size="sm" disabled={loading}>
            {t('quota_audit.apply_filters')}
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={handleReset} disabled={loading}>
            {t('quota_audit.reset_filters')}
          </Button>
        </div>
      </form>

      {error && (
        <div className={styles.errorBox} role="alert">
          <strong>{t('quota_audit.load_failed')}</strong>
          <span>{error}</span>
          <Button variant="secondary" size="sm" onClick={refreshCurrent} disabled={loading}>
            {t('quota_audit.retry')}
          </Button>
        </div>
      )}

      {!error && response && rows.length === 0 && serverAccounts.length === 0 && (
        <EmptyState
          title={t('quota_audit.empty_title')}
          description={t('quota_audit.empty_description')}
          action={
            <Button variant="secondary" size="sm" onClick={refreshCurrent} disabled={loading}>
              {t('quota_audit.refresh')}
            </Button>
          }
        />
      )}

      {!error && response && (rows.length > 0 || serverAccounts.length > 0) && (
        !appliedFilters.account ? (
          <section className={styles.accountPicker} aria-label={t('quota_audit.account_picker_title')}>
            <div className={styles.accountPickerIntro}>
              <h2>{t('quota_audit.account_picker_title')}</h2>
              <p>{t('quota_audit.account_picker_hint')}</p>
            </div>
            <div className={styles.accountCardGrid}>
              {accountSummaries.map((account) => (
                <button
                  type="button"
                  className={`${styles.accountCard} ${account.disabled ? styles.accountCardDisabled : ''}`}
                  key={account.value}
                  onClick={() => handleSelectAccount(account.value)}
                >
                  <strong>{account.label}</strong>
                  <span>{t('quota_audit.account_card_samples', { count: account.samples })}</span>
                  <span>{t('quota_audit.account_card_latest', { percent: formatPercent(account.usedPercent) })}</span>
                  {account.disabled && <span>{t('quota_audit.account_card_disabled')}</span>}
                </button>
              ))}
            </div>
          </section>
        ) : (
        <>
          <div className={styles.selectedAccountBar}>
            <Button type="button" variant="ghost" size="sm" onClick={handleBackToAccounts}>
              {t('quota_audit.account_back')}
            </Button>
            <strong>
              {accountLabels.get(appliedFilters.account) ||
                accounts.find((account) => account.value === appliedFilters.account)?.label ||
                appliedFilters.account}
            </strong>
          </div>
          <section className={styles.summaryGrid} aria-label={t('quota_audit.summary_title')}>
            <div className={styles.summaryCard}>
              <span className={styles.summaryLabel}>{t('quota_audit.summary_accounts')}</span>
              <strong>{formatNumber(summary.accounts)}</strong>
              <small>{t('quota_audit.summary_accounts_hint')}</small>
            </div>
            <div className={styles.summaryCard}>
              <span className={styles.summaryLabel}>{t('quota_audit.summary_samples')}</span>
              <strong>{formatNumber(summary.samples)}</strong>
              <small>{formatPercent(summary.usedPercent)} {t('quota_audit.summary_avg_used')}</small>
            </div>
            <div className={styles.summaryCard}>
              <span className={styles.summaryLabel}>{t('quota_audit.summary_quota_delta')}</span>
              <strong>{formatSignedPercent(summary.quotaDeltaPercent)}</strong>
              <small>{formatNumber(summary.windows)} {t('quota_audit.summary_windows')}</small>
            </div>
            <div className={styles.summaryCard}>
              <span className={styles.summaryLabel}>{t('quota_audit.summary_cost')}</span>
              <strong>{formatCurrency(summary.costDeltaUsd)}</strong>
              <small>{formatNumber(summary.totalTokens)} {t('quota_audit.summary_tokens')}</small>
            </div>
            <div className={styles.summaryCard}>
              <span className={styles.summaryLabel}>{t('quota_audit.summary_quality')}</span>
              <strong>{formatNumber(summary.staleSamples + summary.resetSamples)}</strong>
              <small>
                {formatNumber(summary.staleSamples)} {t('quota_audit.status_stale')} · {formatNumber(summary.resetSamples)} {t('quota_audit.status_reset')}
              </small>
            </div>
          </section>

          <div className={styles.priceNotice}>
            <strong>{t('quota_audit.price_snapshot_title')}</strong>
            <span>
              {priceSnapshot
                ? t('quota_audit.price_snapshot_source', {
                    source: priceSnapshot.source || t('quota_audit.server_source'),
                    capturedAt: priceSnapshot.captured_at
                      ? formatTimestamp(priceSnapshot.captured_at, i18n.language)
                      : t('quota_audit.snapshot_time_unknown')
                  })
                : t('quota_audit.price_snapshot_missing')}
            </span>
          </div>

          <section className={styles.accountChartSections} aria-label={t('quota_audit.trends_title')}>
            {accountCharts.length === 0 && (
              <div className={styles.chartCard}>
                <p className={styles.chartEmpty}>{t('quota_audit.no_trend')}</p>
              </div>
            )}
            {accountCharts.map((account) => (
              <div className={styles.accountChartGroup} key={account.identity}>
                <div className={styles.accountChartHeader}>
                  <h2>{account.label}</h2>
                  <span>{t('quota_audit.trends_title')}</span>
                </div>
                <div className={styles.chartGrid}>
                  <div className={styles.chartCard}>
                    <div className={styles.cardHeader}>
                      <h2>{t('quota_audit.quota_trend')}</h2>
                      <span>{t('quota_audit.chart_reset_hint')}</span>
                    </div>
                    <div className={styles.chartCanvas}>
                      <Line
                        data={buildQuotaChartData(account.trend, i18n.language, chartLabels)}
                        options={chartOptions}
                      />
                    </div>
                  </div>
                  <div className={styles.chartCard}>
                    <div className={styles.cardHeader}>
                      <h2>{t('quota_audit.cost_token_trend')}</h2>
                      <span>{t('quota_audit.chart_backend_cost_hint')}</span>
                    </div>
                    <div className={styles.chartCanvas}>
                      <Line
                        data={buildCostTokenChartData(account.trend, i18n.language, chartLabels)}
                        options={chartOptions}
                      />
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </section>

          {visibleRows.length === 0 ? (
            <EmptyState title={t('quota_audit.filtered_empty_title')} description={t('quota_audit.filtered_empty_description')} />
          ) : (
            <section className={styles.tableCard}>
              <div className={styles.cardHeader}>
                <h2>{t('quota_audit.details_title')}</h2>
                <span>{t('quota_audit.rows_count', { count: visibleRows.length })}</span>
              </div>
              <div className={styles.tableScroller}>
                <table>
                  <thead>
                    <tr>
                      <th>{t('quota_audit.table_account')}</th>
                      <th>{t('quota_audit.table_window')}</th>
                      <th>{t('quota_audit.table_model')}</th>
                      <th>{t('quota_audit.table_time')}</th>
                      <th>{t('quota_audit.table_used')}</th>
                      <th>{t('quota_audit.table_delta')}</th>
                      <th>{t('quota_audit.table_tokens')}</th>
                      <th>{t('quota_audit.table_cost')}</th>
                      <th>{t('quota_audit.table_unit_cost')}</th>
                      <th>{t('quota_audit.table_status')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleRows.map((row, index) => {
                      const state = getRowState(row);
                      return (
                        <tr key={`${row.auth}-${row.window}-${row.timestamp}-${index}`}>
                          <td>
                            <strong>{rowAccount(row, accountLabels)}</strong>
                          </td>
                          <td>{row.window}</td>
                          <td>{row.model || 'unknown'}</td>
                          <td>{formatTimestamp(row.timestamp, i18n.language)}</td>
                          <td>{formatPercent(row.used_percent)}</td>
                          <td className={row.quota_delta_percent != null && row.quota_delta_percent < 0 ? styles.negativeValue : undefined}>
                            {formatSignedPercent(row.quota_delta_percent)}
                          </td>
                          <td>
                            <div className={styles.tokenBreakdown}>
                              <span>{t('quota_audit.token_input')}: {formatNumber(row.tokens.input)}</span>
                              <span>{t('quota_audit.token_output')}: {formatNumber(row.tokens.output)}</span>
                              <span>{t('quota_audit.token_reasoning')}: {formatNumber(row.tokens.reasoning)}</span>
                              <span>{t('quota_audit.token_cached')}: {formatNumber(row.tokens.cached)}</span>
                              <b>{t('quota_audit.token_total')}: {formatNumber(row.tokens.total)}</b>
                            </div>
                          </td>
                          <td>{formatCurrency(row.cost_delta_usd)}</td>
                          <td>{formatCurrency(row.cost_per_quota_percent)}</td>
                          <td>
                            <span className={`${styles.statusBadge} ${styles[`status-${state}`]}`}>
                              {statusLabel(state)}
                            </span>
                            {(row.reason || row.reset_at) && (
                              <small className={styles.statusReason}>
                                {row.reason || `${t('quota_audit.reset_at')}: ${formatTimestamp(row.reset_at || '', i18n.language)}`}
                              </small>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </section>
          )}
        </>
        )
      )}
    </div>
  );
}
