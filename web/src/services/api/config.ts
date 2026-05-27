/**
 * 配置相关 API
 */

import { apiClient } from './client';
import type { Config, SessionRoutingConfig } from '@/types';
import { normalizeConfigResponse } from './transformers';

const normalizeSessionRoutingConfig = (raw: any): SessionRoutingConfig => {
  if (!raw || typeof raw !== 'object') {
    return { enabled: false };
  }
  return {
    enabled: Boolean(raw.enabled),
    providers: Array.isArray(raw.providers)
      ? raw.providers.map((item: unknown) => String(item ?? '').trim()).filter(Boolean)
      : undefined,
    ttlSeconds: raw.ttlSeconds ?? raw['ttl-seconds'],
    failureThreshold: raw.failureThreshold ?? raw['failure-threshold'],
    cooldownSeconds: raw.cooldownSeconds ?? raw['cooldown-seconds'],
    loadWindowSeconds: raw.loadWindowSeconds ?? raw['load-window-seconds'],
    loadWeight: raw.loadWeight ?? raw['load-weight'],
    healthWindowRequests: raw.healthWindowRequests ?? raw['health-window-requests'],
    weightSuccessRate: raw.weightSuccessRate ?? raw['weight-success-rate'],
    weightQuota: raw.weightQuota ?? raw['weight-quota'],
    penaltyStatus429: raw.penaltyStatus429 ?? raw['penalty-status-429'],
    penaltyStatus403: raw.penaltyStatus403 ?? raw['penalty-status-403'],
    penaltyStatus5xx: raw.penaltyStatus5xx ?? raw['penalty-status-5xx'],
  };
};

const toSessionRoutingPayload = (session: SessionRoutingConfig): Record<string, any> => ({
  enabled: Boolean(session.enabled),
  providers: Array.isArray(session.providers)
    ? session.providers.map((item) => String(item ?? '').trim()).filter(Boolean)
    : undefined,
  'ttl-seconds': session.ttlSeconds,
  'failure-threshold': session.failureThreshold,
  'cooldown-seconds': session.cooldownSeconds,
  'load-window-seconds': session.loadWindowSeconds,
  'load-weight': session.loadWeight,
  'health-window-requests': session.healthWindowRequests,
  'weight-success-rate': session.weightSuccessRate,
  'weight-quota': session.weightQuota,
  'penalty-status-429': session.penaltyStatus429,
  'penalty-status-403': session.penaltyStatus403,
  'penalty-status-5xx': session.penaltyStatus5xx,
});

export type RemoteManagementUploadKeyStatus = {
  configured: boolean;
  'upload-key-configured'?: boolean;
};

export const configApi = {
  /**
   * 获取配置（会进行字段规范化）
   */
  async getConfig(): Promise<Config> {
    const raw = await apiClient.get('/config');
    return normalizeConfigResponse(raw);
  },

  /**
   * 获取原始配置（不做转换）
   */
  getRawConfig: () => apiClient.get('/config'),

  /**
   * 更新 Debug 模式
   */
  updateDebug: (enabled: boolean) => apiClient.put('/debug', { value: enabled }),

  /**
   * 更新代理 URL
   */
  updateProxyUrl: (proxyUrl: string) => apiClient.put('/proxy-url', { value: proxyUrl }),

  /**
   * 清除代理 URL
   */
  clearProxyUrl: () => apiClient.delete('/proxy-url'),

  /**
   * 获取 auth file 上传专用 key 配置状态（不会返回密钥内容）
   */
  async getRemoteManagementUploadKeyStatus(): Promise<RemoteManagementUploadKeyStatus> {
    const data = await apiClient.get('/remote-management/upload-key');
    const configured = Boolean(data?.configured ?? data?.['upload-key-configured']);
    return { configured, 'upload-key-configured': configured };
  },

  /**
   * 更新 auth file 上传专用 key。服务端只保存哈希。
   */
  updateRemoteManagementUploadKey: (value: string) =>
    apiClient.put('/remote-management/upload-key', { value }),

  /**
   * 清除 auth file 上传专用 key。
   */
  clearRemoteManagementUploadKey: () => apiClient.delete('/remote-management/upload-key'),

  /**
   * 更新重试次数
   */
  updateRequestRetry: (retryCount: number) => apiClient.put('/request-retry', { value: retryCount }),

  /**
   * 配额回退：切换项目
   */
  updateSwitchProject: (enabled: boolean) =>
    apiClient.put('/quota-exceeded/switch-project', { value: enabled }),

  /**
   * 配额回退：切换预览模型
   */
  updateSwitchPreviewModel: (enabled: boolean) =>
    apiClient.put('/quota-exceeded/switch-preview-model', { value: enabled }),

  /**
   * 使用统计开关
   */
  updateUsageStatistics: (enabled: boolean) =>
    apiClient.put('/usage-statistics-enabled', { value: enabled }),

  /**
   * 请求日志开关
   */
  updateRequestLog: (enabled: boolean) => apiClient.put('/request-log', { value: enabled }),

  /**
   * 写日志到文件开关
   */
  updateLoggingToFile: (enabled: boolean) => apiClient.put('/logging-to-file', { value: enabled }),

  /**
   * 获取日志总大小上限（MB）
   */
  async getLogsMaxTotalSizeMb(): Promise<number> {
    const data = await apiClient.get('/logs-max-total-size-mb');
    return data?.['logs-max-total-size-mb'] ?? data?.logsMaxTotalSizeMb ?? 0;
  },

  /**
   * 更新日志总大小上限（MB）
   */
  updateLogsMaxTotalSizeMb: (value: number) =>
    apiClient.put('/logs-max-total-size-mb', { value }),

  /**
   * WebSocket 鉴权开关
   */
  updateWsAuth: (enabled: boolean) => apiClient.put('/ws-auth', { value: enabled }),

  /**
   * 获取强制模型前缀开关
   */
  async getForceModelPrefix(): Promise<boolean> {
    const data = await apiClient.get('/force-model-prefix');
    return data?.['force-model-prefix'] ?? data?.forceModelPrefix ?? false;
  },

  /**
   * 更新强制模型前缀开关
   */
  updateForceModelPrefix: (enabled: boolean) => apiClient.put('/force-model-prefix', { value: enabled }),

  /**
   * 获取路由策略
   */
  async getRoutingStrategy(): Promise<string> {
    const data = await apiClient.get('/routing/strategy');
    return data?.strategy ?? data?.['routing-strategy'] ?? data?.routingStrategy ?? 'round-robin';
  },

  /**
   * 更新路由策略
   */
  updateRoutingStrategy: (strategy: string) => apiClient.put('/routing/strategy', { value: strategy }),

  /**
   * 获取会话路由配置
   */
  async getRoutingSession(): Promise<SessionRoutingConfig> {
    const data = await apiClient.get('/routing/session');
    return normalizeSessionRoutingConfig(data);
  },

  /**
   * 更新会话路由配置
   */
  updateRoutingSession: (session: SessionRoutingConfig) =>
    apiClient.put('/routing/session', toSessionRoutingPayload(session)),
};
