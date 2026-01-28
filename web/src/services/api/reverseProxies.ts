/**
 * 反向代理管理 API
 */

import { apiClient } from './client';

export interface ReverseProxy {
  id: string;
  name: string;
  'base-url': string;
  enabled?: boolean;
  description?: string;
  headers?: Record<string, string>;
  timeout?: number;
  'created-at'?: string;
}

export interface ProxyRouting {
  codex?: string;
  antigravity?: string;
  claude?: string;
  gemini?: string;
  'gemini-cli'?: string;
  vertex?: string;
  aistudio?: string;
  qwen?: string;
  iflow?: string;
}

export type ProxyRoutingAuth = Record<string, string>;

export const reverseProxiesApi = {
  async list(): Promise<ReverseProxy[]> {
    const data = await apiClient.get('/reverse-proxies');
    return data['reverse-proxies'] || [];
  },

  async create(proxy: Omit<ReverseProxy, 'id' | 'created-at'>): Promise<ReverseProxy> {
    const response = await apiClient.post('/reverse-proxies', proxy);
    return response.proxy;
  },

  async update(id: string, proxy: Partial<ReverseProxy>): Promise<ReverseProxy> {
    const response = await apiClient.put(`/reverse-proxies/${id}`, proxy);
    return response.proxy;
  },

  async delete(id: string): Promise<void> {
    await apiClient.delete(`/reverse-proxies/${id}`);
  },

  async getRouting(): Promise<ProxyRouting> {
    const data = await apiClient.get('/proxy-routing');
    return data['proxy-routing'] || {};
  },

  async updateRouting(routing: ProxyRouting): Promise<ProxyRouting> {
    const response = await apiClient.put('/proxy-routing', routing);
    return response['proxy-routing'];
  },

  async getRoutingAuth(): Promise<ProxyRoutingAuth> {
    const data = await apiClient.get('/proxy-routing-auth');
    return data['proxy-routing-auth'] || {};
  },

  async updateRoutingAuth(routing: ProxyRoutingAuth): Promise<ProxyRoutingAuth> {
    const response = await apiClient.put('/proxy-routing-auth', routing);
    return response['proxy-routing-auth'] || {};
  }
};
