import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { configApi } from '@/services/api';
import type { Config, SessionRoutingConfig } from '@/types';
import styles from './Settings/Settings.module.scss';

type PendingKey =
  | 'debug'
  | 'proxy'
  | 'retry'
  | 'logsMaxSize'
  | 'forceModelPrefix'
  | 'routingStrategy'
  | 'sessionRouting'
  | 'switchProject'
  | 'switchPreview'
  | 'usage'
  | 'loggingToFile'
  | 'wsAuth'
  | 'uploadKey';

export function SettingsPage() {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const updateConfigValue = useConfigStore((state) => state.updateConfigValue);
  const clearCache = useConfigStore((state) => state.clearCache);

  const [loading, setLoading] = useState(true);
  const [proxyValue, setProxyValue] = useState('');
  const [retryValue, setRetryValue] = useState(0);
  const [logsMaxTotalSizeMb, setLogsMaxTotalSizeMb] = useState(0);
  const [routingStrategy, setRoutingStrategy] = useState('round-robin');
  const [sessionProvidersText, setSessionProvidersText] = useState('');
  const [sessionConfig, setSessionConfig] = useState<SessionRoutingConfig>({ enabled: false });
  const [uploadKeyValue, setUploadKeyValue] = useState('');
  const [uploadKeyConfigured, setUploadKeyConfigured] = useState(false);
  const [pending, setPending] = useState<Record<PendingKey, boolean>>({} as Record<PendingKey, boolean>);
  const [error, setError] = useState('');

  const disableControls = connectionStatus !== 'connected';
  // 向后兼容：如果 session.enabled = true，自动切换到 session 模式
  const strategyValue = sessionConfig.enabled ? 'session' : routingStrategy;
  const parsedSessionProviders = useMemo(
    () =>
      sessionProvidersText
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean),
    [sessionProvidersText]
  );

  useEffect(() => {
    const load = async () => {
      setLoading(true);
      setError('');
      try {
        const [
          configResult,
          logsResult,
          prefixResult,
          routingResult,
          sessionResult,
          uploadKeyResult,
        ] = await Promise.allSettled([
          fetchConfig(),
          configApi.getLogsMaxTotalSizeMb(),
          configApi.getForceModelPrefix(),
          configApi.getRoutingStrategy(),
          configApi.getRoutingSession(),
          configApi.getRemoteManagementUploadKeyStatus(),
        ]);

        if (configResult.status !== 'fulfilled') {
          throw configResult.reason;
        }

        const data = configResult.value as Config;
        setProxyValue(data?.proxyUrl ?? '');
        setRetryValue(typeof data?.requestRetry === 'number' ? data.requestRetry : 0);

        if (logsResult.status === 'fulfilled' && Number.isFinite(logsResult.value)) {
          setLogsMaxTotalSizeMb(Math.max(0, Number(logsResult.value)));
          updateConfigValue('logs-max-total-size-mb', Math.max(0, Number(logsResult.value)));
        }

        if (prefixResult.status === 'fulfilled') {
          updateConfigValue('force-model-prefix', Boolean(prefixResult.value));
        }

        if (routingResult.status === 'fulfilled' && routingResult.value) {
          setRoutingStrategy(String(routingResult.value));
          updateConfigValue('routing/strategy', String(routingResult.value));
        }

        if (sessionResult.status === 'fulfilled' && sessionResult.value) {
          const normalized = {
            ...sessionResult.value,
            providers: Array.isArray(sessionResult.value.providers)
              ? sessionResult.value.providers
              : [],
          };
          setSessionConfig(normalized);
          setSessionProvidersText((normalized.providers || []).join(', '));
          updateConfigValue('routing/session', normalized);
        }

        if (uploadKeyResult.status === 'fulfilled') {
          setUploadKeyConfigured(Boolean(uploadKeyResult.value?.configured));
        }
      } catch (err: any) {
        setError(err?.message || t('notification.refresh_failed'));
      } finally {
        setLoading(false);
      }
    };

    load();
  }, [fetchConfig, t, updateConfigValue]);

  useEffect(() => {
    if (config) {
      setProxyValue(config.proxyUrl ?? '');
      if (typeof config.requestRetry === 'number') {
        setRetryValue(config.requestRetry);
      }
      if (typeof config.logsMaxTotalSizeMb === 'number') {
        setLogsMaxTotalSizeMb(config.logsMaxTotalSizeMb);
      }
      if (config.routingStrategy) {
        setRoutingStrategy(config.routingStrategy);
      }
      if (config.routingSession) {
        const normalized = {
          ...config.routingSession,
          providers: Array.isArray(config.routingSession.providers)
            ? config.routingSession.providers
            : [],
        };
        setSessionConfig(normalized);
        setSessionProvidersText((normalized.providers || []).join(', '));
      }
    }
  }, [config?.proxyUrl, config?.requestRetry, config?.logsMaxTotalSizeMb, config?.routingStrategy, config?.routingSession]);

  const setPendingFlag = (key: PendingKey, value: boolean) => {
    setPending((prev) => ({ ...prev, [key]: value }));
  };

  const toggleSetting = async (
    section: PendingKey,
    rawKey: 'debug' | 'usage-statistics-enabled' | 'logging-to-file' | 'ws-auth' | 'force-model-prefix',
    value: boolean,
    updater: (val: boolean) => Promise<any>,
    successMessage: string
  ) => {
    const previous = (() => {
      switch (rawKey) {
        case 'debug':
          return config?.debug ?? false;
        case 'usage-statistics-enabled':
          return config?.usageStatisticsEnabled ?? false;
        case 'logging-to-file':
          return config?.loggingToFile ?? false;
        case 'ws-auth':
          return config?.wsAuth ?? false;
        case 'force-model-prefix':
          return config?.forceModelPrefix ?? false;
        default:
          return false;
      }
    })();

    setPendingFlag(section, true);
    updateConfigValue(rawKey, value);

    try {
      await updater(value);
      clearCache(rawKey);
      showNotification(successMessage, 'success');
    } catch (err: any) {
      updateConfigValue(rawKey, previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag(section, false);
    }
  };

  const handleProxyUpdate = async () => {
    const previous = config?.proxyUrl ?? '';
    setPendingFlag('proxy', true);
    updateConfigValue('proxy-url', proxyValue);
    try {
      await configApi.updateProxyUrl(proxyValue.trim());
      clearCache('proxy-url');
      showNotification(t('notification.proxy_updated'), 'success');
    } catch (err: any) {
      setProxyValue(previous);
      updateConfigValue('proxy-url', previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('proxy', false);
    }
  };

  const handleProxyClear = async () => {
    const previous = config?.proxyUrl ?? '';
    setPendingFlag('proxy', true);
    updateConfigValue('proxy-url', '');
    try {
      await configApi.clearProxyUrl();
      clearCache('proxy-url');
      setProxyValue('');
      showNotification(t('notification.proxy_cleared'), 'success');
    } catch (err: any) {
      setProxyValue(previous);
      updateConfigValue('proxy-url', previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('proxy', false);
    }
  };

  const handleRetryUpdate = async () => {
    const previous = config?.requestRetry ?? 0;
    const parsed = Number(retryValue);
    if (!Number.isFinite(parsed) || parsed < 0) {
      showNotification(t('login.error_invalid'), 'error');
      setRetryValue(previous);
      return;
    }
    setPendingFlag('retry', true);
    updateConfigValue('request-retry', parsed);
    try {
      await configApi.updateRequestRetry(parsed);
      clearCache('request-retry');
      showNotification(t('notification.retry_updated'), 'success');
    } catch (err: any) {
      setRetryValue(previous);
      updateConfigValue('request-retry', previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('retry', false);
    }
  };

  const handleLogsMaxTotalSizeUpdate = async () => {
    const previous = config?.logsMaxTotalSizeMb ?? 0;
    const parsed = Number(logsMaxTotalSizeMb);
    if (!Number.isFinite(parsed) || parsed < 0) {
      showNotification(t('login.error_invalid'), 'error');
      setLogsMaxTotalSizeMb(previous);
      return;
    }
    const normalized = Math.max(0, parsed);
    setPendingFlag('logsMaxSize', true);
    updateConfigValue('logs-max-total-size-mb', normalized);
    try {
      await configApi.updateLogsMaxTotalSizeMb(normalized);
      clearCache('logs-max-total-size-mb');
      showNotification(t('notification.logs_max_total_size_updated'), 'success');
    } catch (err: any) {
      setLogsMaxTotalSizeMb(previous);
      updateConfigValue('logs-max-total-size-mb', previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('logsMaxSize', false);
    }
  };

  const handleUploadKeyUpdate = async () => {
    const value = uploadKeyValue.trim();
    if (!value) {
      showNotification(t('login.error_invalid'), 'error');
      return;
    }
    setPendingFlag('uploadKey', true);
    try {
      await configApi.updateRemoteManagementUploadKey(value);
      setUploadKeyValue('');
      setUploadKeyConfigured(true);
      showNotification(t('notification.upload_key_updated'), 'success');
    } catch (err: any) {
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('uploadKey', false);
    }
  };

  const handleUploadKeyClear = async () => {
    setPendingFlag('uploadKey', true);
    try {
      await configApi.clearRemoteManagementUploadKey();
      setUploadKeyValue('');
      setUploadKeyConfigured(false);
      showNotification(t('notification.upload_key_cleared'), 'success');
    } catch (err: any) {
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('uploadKey', false);
    }
  };

  const handleRoutingStrategyUpdate = async () => {
    const strategy = strategyValue.trim();
    if (!strategy) {
      showNotification(t('login.error_invalid'), 'error');
      return;
    }
    const previous = config?.routingStrategy ?? 'round-robin';
    setPendingFlag('routingStrategy', true);

    try {
      if (strategy === 'session') {
        // 切换到 session 模式：更新 routing.strategy = "session"
        await configApi.updateRoutingStrategy('session');
        setRoutingStrategy('session');
        updateConfigValue('routing/strategy', 'session');
        clearCache('routing/strategy');
      } else {
        // 切换到 round-robin 或 fill-first
        await configApi.updateRoutingStrategy(strategy);
        setRoutingStrategy(strategy);
        updateConfigValue('routing/strategy', strategy);
        clearCache('routing/strategy');
      }
      showNotification(t('notification.routing_strategy_updated'), 'success');
    } catch (err: any) {
      setRoutingStrategy(previous);
      updateConfigValue('routing/strategy', previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('routingStrategy', false);
    }
  };

  const handleSessionRoutingUpdate = async () => {
    const previous = { ...sessionConfig };
    const normalizedConfig: SessionRoutingConfig = {
      ...sessionConfig,
      providers: parsedSessionProviders,
    };
    setPendingFlag('sessionRouting', true);
    updateConfigValue('routing/session', normalizedConfig);
    try {
      await configApi.updateRoutingSession(normalizedConfig);
      setSessionConfig(normalizedConfig);
      clearCache('routing/session');
      showNotification(t('notification.session_routing_updated'), 'success');
    } catch (err: any) {
      setSessionConfig(previous);
      updateConfigValue('routing/session', previous);
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setPendingFlag('sessionRouting', false);
    }
  };

  const updateSessionField = <K extends keyof SessionRoutingConfig>(field: K, value: SessionRoutingConfig[K]) => {
    setSessionConfig((prev) => ({ ...prev, [field]: value }));
  };

  const quotaSwitchProject = config?.quotaExceeded?.switchProject ?? false;
  const quotaSwitchPreview = config?.quotaExceeded?.switchPreviewModel ?? false;

  return (
    <div className={styles.container}>
      <h1 className={styles.pageTitle}>{t('basic_settings.title')}</h1>

      <div className={styles.grid}>
        <Card>
          {error && <div className="error-box">{error}</div>}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <ToggleSwitch
              label={t('basic_settings.debug_enable')}
              checked={config?.debug ?? false}
              disabled={disableControls || pending.debug || loading}
              onChange={(value) =>
                toggleSetting('debug', 'debug', value, configApi.updateDebug, t('notification.debug_updated'))
              }
            />

            <ToggleSwitch
              label={t('basic_settings.usage_statistics_enable')}
              checked={config?.usageStatisticsEnabled ?? false}
              disabled={disableControls || pending.usage || loading}
              onChange={(value) =>
                toggleSetting(
                  'usage',
                  'usage-statistics-enabled',
                  value,
                  configApi.updateUsageStatistics,
                  t('notification.usage_statistics_updated')
                )
              }
            />

            <ToggleSwitch
              label={t('basic_settings.logging_to_file_enable')}
              checked={config?.loggingToFile ?? false}
              disabled={disableControls || pending.loggingToFile || loading}
              onChange={(value) =>
                toggleSetting(
                  'loggingToFile',
                  'logging-to-file',
                  value,
                  configApi.updateLoggingToFile,
                  t('notification.logging_to_file_updated')
                )
              }
            />

            <ToggleSwitch
              label={t('basic_settings.ws_auth_enable')}
              checked={config?.wsAuth ?? false}
              disabled={disableControls || pending.wsAuth || loading}
              onChange={(value) =>
                toggleSetting(
                  'wsAuth',
                  'ws-auth',
                  value,
                  configApi.updateWsAuth,
                  t('notification.ws_auth_updated')
                )
              }
            />

            <ToggleSwitch
              label={t('basic_settings.force_model_prefix_enable')}
              checked={config?.forceModelPrefix ?? false}
              disabled={disableControls || pending.forceModelPrefix || loading}
              onChange={(value) =>
                toggleSetting(
                  'forceModelPrefix',
                  'force-model-prefix',
                  value,
                  configApi.updateForceModelPrefix,
                  t('notification.force_model_prefix_updated')
                )
              }
            />
          </div>
        </Card>

      <Card title={t('basic_settings.proxy_title')}>
        <Input
          label={t('basic_settings.proxy_url_label')}
          placeholder={t('basic_settings.proxy_url_placeholder')}
          value={proxyValue}
          onChange={(e) => setProxyValue(e.target.value)}
          disabled={disableControls || loading}
        />
        <div style={{ display: 'flex', gap: 12 }}>
          <Button variant="secondary" onClick={handleProxyClear} disabled={disableControls || pending.proxy || loading}>
            {t('basic_settings.proxy_clear')}
          </Button>
          <Button onClick={handleProxyUpdate} loading={pending.proxy} disabled={disableControls || loading}>
            {t('basic_settings.proxy_update')}
          </Button>
        </div>
      </Card>

      <Card title={t('basic_settings.retry_title')}>
        <div className={styles.retryRow}>
          <Input
            label={t('basic_settings.retry_count_label')}
            type="number"
            inputMode="numeric"
            min={0}
            step={1}
            value={retryValue}
            onChange={(e) => setRetryValue(Number(e.target.value))}
            disabled={disableControls || loading}
            className={styles.retryInput}
          />
          <Button
            className={styles.retryButton}
            onClick={handleRetryUpdate}
            loading={pending.retry}
            disabled={disableControls || loading}
          >
            {t('basic_settings.retry_update')}
          </Button>
        </div>
      </Card>

      <Card title={t('basic_settings.logs_max_total_size_title')}>
        <div className={`${styles.retryRow} ${styles.retryRowAligned} ${styles.retryRowInputGrow}`}>
          <Input
            label={t('basic_settings.logs_max_total_size_label')}
            hint={t('basic_settings.logs_max_total_size_hint')}
            type="number"
            inputMode="numeric"
            min={0}
            step={1}
            value={logsMaxTotalSizeMb}
            onChange={(e) => setLogsMaxTotalSizeMb(Number(e.target.value))}
            disabled={disableControls || loading}
            className={styles.retryInput}
          />
          <Button
            className={styles.retryButton}
            onClick={handleLogsMaxTotalSizeUpdate}
            loading={pending.logsMaxSize}
            disabled={disableControls || loading}
          >
            {t('basic_settings.logs_max_total_size_update')}
          </Button>
        </div>
      </Card>

      <Card title={t('basic_settings.upload_key_title')}>
        <div className={`${styles.retryRow} ${styles.retryRowAligned} ${styles.retryRowInputGrow}`}>
          <Input
            label={t('basic_settings.upload_key_label')}
            hint={t('basic_settings.upload_key_hint')}
            type="password"
            value={uploadKeyValue}
            onChange={(e) => setUploadKeyValue(e.target.value)}
            disabled={disableControls || loading || pending.uploadKey}
            placeholder={t('basic_settings.upload_key_placeholder')}
            className={styles.retryInput}
          />
          <Button
            className={styles.retryButton}
            onClick={handleUploadKeyUpdate}
            loading={pending.uploadKey}
            disabled={disableControls || loading || !uploadKeyValue.trim()}
          >
            {t('basic_settings.upload_key_update')}
          </Button>
        </div>
        <div className={styles.settingFooterRow}>
          <span className={uploadKeyConfigured ? styles.statusOk : styles.statusMuted}>
            {uploadKeyConfigured
              ? t('basic_settings.upload_key_configured')
              : t('basic_settings.upload_key_not_configured')}
          </span>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleUploadKeyClear}
            disabled={disableControls || loading || pending.uploadKey || !uploadKeyConfigured}
          >
            {t('basic_settings.upload_key_clear')}
          </Button>
        </div>
      </Card>

      <Card title={t('basic_settings.routing_title')}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div className="form-group">
            <label>{t('basic_settings.routing_strategy_label')}</label>
            <select
              className="input"
              value={strategyValue}
              onChange={(e) => {
                const next = e.target.value;
                if (next === 'session') {
                  setSessionConfig((prev) => ({ ...prev, enabled: true }));
                } else {
                  setRoutingStrategy(next);
                  setSessionConfig((prev) => ({ ...prev, enabled: false }));
                }
              }}
              disabled={disableControls || loading}
            >
              <option value="round-robin">{t('basic_settings.routing_strategy_round_robin')}</option>
              <option value="fill-first">{t('basic_settings.routing_strategy_fill_first')}</option>
              <option value="session">{t('basic_settings.routing_strategy_session')}</option>
            </select>
            <div className="hint">{t('basic_settings.routing_strategy_hint')}</div>
          </div>

          {strategyValue === 'session' && (
            <>
              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_providers_label')}
                  hint={t('basic_settings.session_routing_providers_hint')}
                  value={sessionProvidersText}
                  onChange={(e) => setSessionProvidersText(e.target.value)}
                  disabled={disableControls || loading}
                  placeholder={t('basic_settings.session_routing_providers_placeholder')}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_ttl_label')}
                  hint={t('basic_settings.session_routing_ttl_hint')}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  step={1}
                  value={sessionConfig.ttlSeconds ?? 300}
                  onChange={(e) => updateSessionField('ttlSeconds', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_failure_threshold_label')}
                  hint={t('basic_settings.session_routing_failure_threshold_hint')}
                  type="number"
                  inputMode="numeric"
                  min={1}
                  step={1}
                  value={sessionConfig.failureThreshold ?? 3}
                  onChange={(e) => updateSessionField('failureThreshold', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_cooldown_label')}
                  hint={t('basic_settings.session_routing_cooldown_hint')}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  step={1}
                  value={sessionConfig.cooldownSeconds ?? 60}
                  onChange={(e) => updateSessionField('cooldownSeconds', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_load_window_label')}
                  hint={t('basic_settings.session_routing_load_window_hint')}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  step={1}
                  value={sessionConfig.loadWindowSeconds ?? 600}
                  onChange={(e) => updateSessionField('loadWindowSeconds', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_load_weight_label')}
                  hint={t('basic_settings.session_routing_load_weight_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  max={1}
                  step={0.05}
                  value={sessionConfig.loadWeight ?? 0.25}
                  onChange={(e) => updateSessionField('loadWeight', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_health_window_label')}
                  hint={t('basic_settings.session_routing_health_window_hint')}
                  type="number"
                  inputMode="numeric"
                  min={1}
                  step={1}
                  value={sessionConfig.healthWindowRequests ?? 50}
                  onChange={(e) => updateSessionField('healthWindowRequests', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_weight_success_label')}
                  hint={t('basic_settings.session_routing_weight_success_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  step={0.1}
                  value={sessionConfig.weightSuccessRate ?? 0.6}
                  onChange={(e) => updateSessionField('weightSuccessRate', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_weight_quota_label')}
                  hint={t('basic_settings.session_routing_weight_quota_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  step={0.1}
                  value={sessionConfig.weightQuota ?? 0.4}
                  onChange={(e) => updateSessionField('weightQuota', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_penalty_429_label')}
                  hint={t('basic_settings.session_routing_penalty_429_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  step={0.1}
                  value={sessionConfig.penaltyStatus429 ?? 1}
                  onChange={(e) => updateSessionField('penaltyStatus429', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_penalty_403_label')}
                  hint={t('basic_settings.session_routing_penalty_403_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  step={0.1}
                  value={sessionConfig.penaltyStatus403 ?? 0.7}
                  onChange={(e) => updateSessionField('penaltyStatus403', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_penalty_5xx_label')}
                  hint={t('basic_settings.session_routing_penalty_5xx_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  step={0.1}
                  value={sessionConfig.penaltyStatus5xx ?? 0.4}
                  onChange={(e) => updateSessionField('penaltyStatus5xx', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className={styles.retryRow}>
                <Input
                  label={t('basic_settings.session_routing_penalty_exponent_label')}
                  hint={t('basic_settings.session_routing_penalty_exponent_hint')}
                  type="number"
                  inputMode="decimal"
                  min={0.1}
                  max={5}
                  step={0.1}
                  value={sessionConfig.penaltyExponent ?? 1.0}
                  onChange={(e) => updateSessionField('penaltyExponent', Number(e.target.value))}
                  disabled={disableControls || loading}
                  className={styles.retryInput}
                />
              </div>

              <div className="form-group">
                <label>{t('basic_settings.session_routing_load_balance_mode_label')}</label>
                <select
                  className="input"
                  value={sessionConfig.loadBalanceMode ?? 'exponential'}
                  onChange={(e) => updateSessionField('loadBalanceMode', e.target.value)}
                  disabled={disableControls || loading}
                >
                  <option value="exponential">{t('basic_settings.session_routing_load_balance_mode_exponential')}</option>
                  <option value="linear">{t('basic_settings.session_routing_load_balance_mode_linear')}</option>
                </select>
                <div className="hint">{t('basic_settings.session_routing_load_balance_mode_hint')}</div>
              </div>
            </>
          )}

          <Button
            onClick={strategyValue === 'session' ? handleSessionRoutingUpdate : handleRoutingStrategyUpdate}
            loading={pending.routingStrategy || pending.sessionRouting}
            disabled={disableControls || loading}
          >
            {t('basic_settings.routing_strategy_update')}
          </Button>
        </div>
      </Card>

      <Card title={t('basic_settings.quota_title')}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <ToggleSwitch
            label={t('basic_settings.quota_switch_project')}
            checked={quotaSwitchProject}
            disabled={disableControls || pending.switchProject || loading}
            onChange={(value) =>
              (async () => {
                const previous = config?.quotaExceeded?.switchProject ?? false;
                const nextQuota = { ...(config?.quotaExceeded || {}), switchProject: value };
                setPendingFlag('switchProject', true);
                updateConfigValue('quota-exceeded', nextQuota);
                try {
                  await configApi.updateSwitchProject(value);
                  clearCache('quota-exceeded');
                  showNotification(t('notification.quota_switch_project_updated'), 'success');
                } catch (err: any) {
                  updateConfigValue('quota-exceeded', { ...(config?.quotaExceeded || {}), switchProject: previous });
                  showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
                } finally {
                  setPendingFlag('switchProject', false);
                }
              })()
            }
          />
          <ToggleSwitch
            label={t('basic_settings.quota_switch_preview')}
            checked={quotaSwitchPreview}
            disabled={disableControls || pending.switchPreview || loading}
            onChange={(value) =>
              (async () => {
                const previous = config?.quotaExceeded?.switchPreviewModel ?? false;
                const nextQuota = { ...(config?.quotaExceeded || {}), switchPreviewModel: value };
                setPendingFlag('switchPreview', true);
                updateConfigValue('quota-exceeded', nextQuota);
                try {
                  await configApi.updateSwitchPreviewModel(value);
                  clearCache('quota-exceeded');
                  showNotification(t('notification.quota_switch_preview_updated'), 'success');
                } catch (err: any) {
                  updateConfigValue('quota-exceeded', { ...(config?.quotaExceeded || {}), switchPreviewModel: previous });
                  showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
                } finally {
                  setPendingFlag('switchPreview', false);
                }
              })()
            }
          />
        </div>
      </Card>
      </div>
    </div>
  );
}
