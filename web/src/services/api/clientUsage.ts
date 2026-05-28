import axios from 'axios';
import { normalizeApiBase } from '@/utils/connection';

const CLIENT_API_PREFIX = '/v0/client';
const CLIENT_USAGE_TIMEOUT_MS = 60 * 1000;

export interface ClientUsageTotals {
  total_requests: number;
  success_count: number;
  failure_count: number;
  total_tokens: number;
}

export interface ClientModelUsage {
  model: string;
  total_requests: number;
  total_tokens: number;
}

export interface ClientAuthFileUsage {
  auth_id: string;
  auth_index: string;
  provider: string;
  type?: string;
  platform?: string;
  label?: string;
  file_name?: string;
  account_type?: string;
  account?: string;
  status?: string;
  status_message?: string;
  disabled?: boolean;
  unavailable?: boolean;
  last_refresh?: string;
  next_retry_after?: string;
  quota?: {
    exceeded?: boolean;
    reason?: string;
    next_recover_at?: string;
    backoff_level?: number;
  };
  success?: number;
  failed?: number;
  recent_requests?: Array<{ time?: string; success: number; failed: number }>;
  usage: ClientUsageTotals;
  models?: ClientModelUsage[];
}

export interface ClientAuthFileUsageResponse {
  usage_statistics_enabled: boolean;
  restricted: boolean;
  totals: ClientUsageTotals;
  auth_files: ClientAuthFileUsage[];
}

const buildClientBaseURL = (apiBase: string): string => {
  const normalized = normalizeApiBase(apiBase);
  if (!normalized) return CLIENT_API_PREFIX;
  return `${normalized}${CLIENT_API_PREFIX}`;
};

export const clientUsageApi = {
  async getAuthFileUsage(apiBase: string, apiKey: string): Promise<ClientAuthFileUsageResponse> {
    const baseURL = buildClientBaseURL(apiBase);
    const response = await axios.get<ClientAuthFileUsageResponse>('/usage/auth-files', {
      baseURL,
      timeout: CLIENT_USAGE_TIMEOUT_MS,
      headers: {
        Authorization: `Bearer ${apiKey}`
      }
    });
    return response.data;
  }
};
