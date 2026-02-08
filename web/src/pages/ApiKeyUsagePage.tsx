import React, { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import { useLanguageStore } from '@/stores';
import { clientUsageApi, type ClientAuthFileUsageResponse } from '@/services/api/clientUsage';
import { detectApiBaseFromLocation, normalizeApiBase } from '@/utils/connection';
import styles from './ApiKeyUsagePage.module.scss';

export function ApiKeyUsagePage() {
  const { t } = useTranslation();
  const language = useLanguageStore((state) => state.language);
  const toggleLanguage = useLanguageStore((state) => state.toggleLanguage);
  const nextLanguageLabel = language === 'zh-CN' ? t('language.english') : t('language.chinese');

  const detectedBase = useMemo(() => detectApiBaseFromLocation(), []);

  const [apiBase, setApiBase] = useState('');
  const [showCustomBase, setShowCustomBase] = useState(false);

  const [apiKey, setApiKey] = useState('');
  const [showKey, setShowKey] = useState(false);

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [data, setData] = useState<ClientAuthFileUsageResponse | null>(null);

  const effectiveBase = useMemo(() => {
    const baseToUse = apiBase ? normalizeApiBase(apiBase) : detectedBase;
    return baseToUse || detectedBase;
  }, [apiBase, detectedBase]);

  const loadUsage = useCallback(async () => {
    const trimmed = apiKey.trim();
    if (!trimmed) {
      setError(t('api_key_usage.error_required'));
      return;
    }

    setLoading(true);
    setError('');
    try {
      const result = await clientUsageApi.getAuthFileUsage(effectiveBase, trimmed);
      setData(result);
    } catch (err: any) {
      setData(null);
      setError(err?.message || t('api_key_usage.error_failed'));
    } finally {
      setLoading(false);
    }
  }, [apiKey, effectiveBase, t]);

  const handleSubmitKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'Enter' && !loading) {
        event.preventDefault();
        void loadUsage();
      }
    },
    [loading, loadUsage]
  );

  return (
    <div className={`login-page ${styles.page}`}>
      <div className="login-card">
        <div className="login-header">
          <div className="login-title-row">
            <div className="title">{t('api_key_usage.title')}</div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="login-language-btn"
              onClick={toggleLanguage}
              title={t('language.switch')}
              aria-label={t('language.switch')}
            >
              {nextLanguageLabel}
            </Button>
          </div>
          <div className="subtitle">{t('api_key_usage.subtitle')}</div>
        </div>

        <div className="connection-box">
          <div className="label">{t('login.connection_current')}</div>
          <div className="value">{effectiveBase}</div>
          <div className="hint">{t('login.connection_auto_hint')}</div>
        </div>

        <div className="toggle-advanced">
          <input
            id="custom-connection-toggle-usage"
            type="checkbox"
            checked={showCustomBase}
            onChange={(e) => setShowCustomBase(e.target.checked)}
          />
          <label htmlFor="custom-connection-toggle-usage">{t('login.custom_connection_label')}</label>
        </div>

        {showCustomBase && (
          <Input
            label={t('login.custom_connection_label')}
            placeholder={t('login.custom_connection_placeholder')}
            value={apiBase}
            onChange={(e) => setApiBase(e.target.value)}
            hint={t('login.custom_connection_hint')}
          />
        )}

        <Input
          autoFocus
          label={t('api_key_usage.api_key_label')}
          placeholder={t('api_key_usage.api_key_placeholder')}
          type={showKey ? 'text' : 'password'}
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          onKeyDown={handleSubmitKeyDown}
          rightElement={
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => setShowKey((prev) => !prev)}
              aria-label={showKey ? t('login.hide_key') : t('login.show_key')}
              title={showKey ? t('login.hide_key') : t('login.show_key')}
            >
              {showKey ? <IconEyeOff size={16} /> : <IconEye size={16} />}
            </button>
          }
        />

        <Button fullWidth onClick={() => void loadUsage()} loading={loading}>
          {loading ? t('api_key_usage.refreshing') : t('api_key_usage.refresh')}
        </Button>

        {error && <div className="error-box">{error}</div>}

        {data && (
          <div className={styles.results}>
            {!data.usage_statistics_enabled && (
              <div className={styles.warning}>{t('api_key_usage.usage_disabled')}</div>
            )}

            <div className={styles.summary}>
              <div>
                {t('api_key_usage.total_requests')}:&nbsp;
                <span className={styles.summaryValue}>{data.totals.total_requests}</span>
              </div>
              <div>
                {t('api_key_usage.total_tokens')}:&nbsp;
                <span className={styles.summaryValue}>{data.totals.total_tokens}</span>
              </div>
              <div>
                {t('api_key_usage.success_count')}:&nbsp;
                <span className={styles.summaryValue}>{data.totals.success_count}</span>
              </div>
            </div>

            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>{t('api_key_usage.table.provider')}</th>
                    <th>{t('api_key_usage.table.auth')}</th>
                    <th>{t('api_key_usage.table.account')}</th>
                    <th>{t('api_key_usage.table.requests')}</th>
                    <th>{t('api_key_usage.table.tokens')}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.auth_files.map((item) => {
                    const key = item.auth_id || item.auth_index;
                    const displayAuth = item.label || item.file_name || item.auth_id || item.auth_index;
                    const account = item.account || '';
                    return (
                      <tr key={key}>
                        <td>{item.provider || '-'}</td>
                        <td>
                          <div>{displayAuth}</div>
                          {item.models && item.models.length > 0 && (
                            <details className={styles.models}>
                              <summary className={styles.muted}>
                                {t('api_key_usage.table.models', { count: item.models.length })}
                              </summary>
                              <div className={styles.tableWrap} style={{ marginTop: 8 }}>
                                <table className={styles.table}>
                                  <thead>
                                    <tr>
                                      <th>{t('api_key_usage.models.model')}</th>
                                      <th className={styles.num}>{t('api_key_usage.models.requests')}</th>
                                      <th className={styles.num}>{t('api_key_usage.models.tokens')}</th>
                                    </tr>
                                  </thead>
                                  <tbody>
                                    {item.models.map((m) => (
                                      <tr key={m.model}>
                                        <td>{m.model}</td>
                                        <td className={styles.num}>{m.total_requests}</td>
                                        <td className={styles.num}>{m.total_tokens}</td>
                                      </tr>
                                    ))}
                                  </tbody>
                                </table>
                              </div>
                            </details>
                          )}
                        </td>
                        <td className={account ? '' : styles.muted}>{account || '-'}</td>
                        <td className={styles.num}>{item.usage?.total_requests ?? 0}</td>
                        <td className={styles.num}>{item.usage?.total_tokens ?? 0}</td>
                      </tr>
                    );
                  })}
                  {data.auth_files.length === 0 && (
                    <tr>
                      <td colSpan={5} className={styles.muted}>
                        {t('api_key_usage.no_accounts')}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
