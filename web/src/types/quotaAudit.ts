export type QuotaAuditStatus =
  | 'ok'
  | 'warning'
  | 'reset'
  | 'unknown'
  | 'negative'
  | 'stale'
  | string;

export interface QuotaAuditTokens {
  input: number;
  output: number;
  reasoning: number;
  cached: number;
  total: number;
}
export interface QuotaAuditPriceSnapshot {
  input_per_million_usd?: number | null;
  output_per_million_usd?: number | null;
  reasoning_per_million_usd?: number | null;
  cached_per_million_usd?: number | null;
  currency?: string;
  captured_at?: string;
  source?: string;
}

export interface QuotaAuditRow {
  auth: string;
  account?: string;
  window: string;
  model?: string;
  timestamp: string;
  used_percent?: number | null;
  quota_delta_percent?: number | null;
  tokens: QuotaAuditTokens;
  cost_delta_usd?: number | null;
  cost_per_quota_percent?: number | null;
  status?: QuotaAuditStatus;
  reset?: boolean;
  reset_at?: string | null;
  stale?: boolean;
  reason?: string | null;
  price_snapshot?: QuotaAuditPriceSnapshot | null;
}

export interface QuotaAuditSummary {
  accounts?: number;
  windows?: number;
  samples?: number;
  used_percent?: number | null;
  quota_delta_percent?: number | null;
  input_tokens?: number;
  output_tokens?: number;
  reasoning_tokens?: number;
  cached_tokens?: number;
  total_tokens?: number;
  cost_delta_usd?: number | null;
  cost_per_quota_percent?: number | null;
  stale_samples?: number;
  reset_samples?: number;
}

export interface QuotaAuditResponse {
  snapshots?: QuotaAuditRow[];
  rows?: QuotaAuditRow[];
  summary?: QuotaAuditSummary;
  price_snapshot?: QuotaAuditPriceSnapshot | null;
}
