import { apiClient } from './client';
import type { QuotaAuditResponse } from '@/types/quotaAudit';

const QUOTA_AUDIT_TIMEOUT_MS = 60 * 1000;

export interface QuotaAuditQuery {
  account?: string;
  auth_index?: string;
  auth?: string;
  window?: string;
  model?: string;
  from?: string;
  to?: string;
}

const compactQuery = (query: QuotaAuditQuery): Record<string, string> =>
  Object.fromEntries(
    Object.entries(query).filter(([, value]) => typeof value === 'string' && value.trim())
  ) as Record<string, string>;

export const quotaAuditApi = {
  getAudit: (query: QuotaAuditQuery = {}) =>
    apiClient.get<QuotaAuditResponse>('/usage/quota-audit', {
      params: compactQuery(query),
      timeout: QUOTA_AUDIT_TIMEOUT_MS
    })
};
