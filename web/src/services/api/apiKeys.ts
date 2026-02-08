/**
 * API 密钥管理
 */

import { apiClient } from './client';

export type APIKeyAuthMap = Record<string, string[]>;
export type APIKeyExpiryMap = Record<string, string>;

export const apiKeysApi = {
  async list(): Promise<string[]> {
    const data = await apiClient.get('/api-keys');
    const keys = (data && (data['api-keys'] ?? data.apiKeys)) as unknown;
    return Array.isArray(keys) ? (keys as string[]) : [];
  },

  replace: (keys: string[]) => apiClient.put('/api-keys', keys),

  update: (index: number, value: string) => apiClient.patch('/api-keys', { index, value }),

  delete: (index: number) => apiClient.delete(`/api-keys?index=${index}`),

  async getAuthMapping(): Promise<APIKeyAuthMap> {
    const data = await apiClient.get('/api-key-auth');
    const mapping = (data && (data['api-key-auth'] ?? data.apiKeyAuth)) as unknown;
    return mapping && typeof mapping === 'object' ? (mapping as APIKeyAuthMap) : {};
  },

  updateAuthMapping: (mapping: APIKeyAuthMap) => apiClient.put('/api-key-auth', mapping),

  async getExpiryMapping(): Promise<APIKeyExpiryMap> {
    const data = await apiClient.get('/api-key-expiry');
    const mapping = (data && (data['api-key-expiry'] ?? data.apiKeyExpiry)) as unknown;
    return mapping && typeof mapping === 'object' ? (mapping as APIKeyExpiryMap) : {};
  },

  updateExpiryMapping: (mapping: APIKeyExpiryMap) => apiClient.put('/api-key-expiry', mapping)
};
