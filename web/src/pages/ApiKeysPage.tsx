import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { EmptyState } from '@/components/ui/EmptyState';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { apiKeysApi, authFilesApi } from '@/services/api';
import type { AuthFileItem } from '@/types/authFile';
import { maskApiKey } from '@/utils/format';
import { isValidApiKeyCharset } from '@/utils/validation';
import styles from './ApiKeysPage.module.scss';

type ScopeMode = 'all' | 'restricted' | 'deny';

const dayMs = 24 * 60 * 60 * 1000;

const remainingDays = (iso?: string): number | null => {
  const trimmed = String(iso ?? '').trim();
  if (!trimmed) return null;
  const d = new Date(trimmed);
  if (Number.isNaN(d.getTime())) return null;
  const diff = d.getTime() - Date.now();
  if (diff <= 0) return 0;
  return Math.ceil(diff / dayMs);
};

const expiryISOFromDays = (days: number): string | null => {
  if (!Number.isFinite(days) || days <= 0) return null;
  return new Date(Date.now() + days * dayMs).toISOString();
};

export function ApiKeysPage() {
  const { t } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);

  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const updateConfigValue = useConfigStore((state) => state.updateConfigValue);
  const clearCache = useConfigStore((state) => state.clearCache);

  const [apiKeys, setApiKeys] = useState<string[]>([]);
  const [apiKeyAuth, setApiKeyAuth] = useState<Record<string, string[]>>({});
  const [apiKeyExpiry, setApiKeyExpiry] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [inputValue, setInputValue] = useState('');
  const [scopeMode, setScopeMode] = useState<ScopeMode>('all');
  const [expiryDays, setExpiryDays] = useState('');
  const [expiryOriginalISO, setExpiryOriginalISO] = useState<string>('');
  const [expiryDirty, setExpiryDirty] = useState(false);
  const [accounts, setAccounts] = useState<AuthFileItem[]>([]);
  const [accountsLoaded, setAccountsLoaded] = useState(false);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [accountSearch, setAccountSearch] = useState('');
  const [selectedAccounts, setSelectedAccounts] = useState<Set<string>>(new Set());
  const [pendingAllowedRefs, setPendingAllowedRefs] = useState<string[] | null>(null);
  const [saving, setSaving] = useState(false);

  const disableControls = useMemo(() => connectionStatus !== 'connected', [connectionStatus]);

  const loadAll = useCallback(
    async (force = false) => {
      setLoading(true);
      setError('');
      try {
        const [keysResult, authResult, expiryResult] = await Promise.all([
          fetchConfig('api-keys', force),
          fetchConfig('api-key-auth', force),
          fetchConfig('api-key-expiry', force),
        ]);

        const list = Array.isArray(keysResult) ? (keysResult as string[]) : [];
        setApiKeys(list);

        const authMap =
          authResult && typeof authResult === 'object' ? (authResult as Record<string, string[]>) : {};
        setApiKeyAuth(authMap || {});

        const expiryMap =
          expiryResult && typeof expiryResult === 'object' ? (expiryResult as Record<string, string>) : {};
        setApiKeyExpiry(expiryMap || {});
      } catch (err: any) {
        setError(err?.message || t('notification.refresh_failed'));
      } finally {
        setLoading(false);
      }
    },
    [fetchConfig, t]
  );

  useEffect(() => {
    loadAll();
  }, [loadAll]);

  useEffect(() => {
    if (Array.isArray(config?.apiKeys)) {
      setApiKeys(config.apiKeys);
    }
  }, [config?.apiKeys]);

  useEffect(() => {
    if (config?.apiKeyAuth && typeof config.apiKeyAuth === 'object') {
      setApiKeyAuth(config.apiKeyAuth);
    }
  }, [config?.apiKeyAuth]);

  useEffect(() => {
    if (config?.apiKeyExpiry && typeof config.apiKeyExpiry === 'object') {
      setApiKeyExpiry(config.apiKeyExpiry);
    }
  }, [config?.apiKeyExpiry]);

  useEffect(() => {
    if (!modalOpen) return;
    if (scopeMode !== 'restricted') return;
    if (accountsLoaded || accountsLoading) return;

    setAccountsLoading(true);
    authFilesApi
      .list()
      .then((res) => {
        setAccounts(res?.files || []);
        setAccountsLoaded(true);
      })
      .catch(() => {
        // Non-fatal: user can still save without selecting accounts.
        setAccounts([]);
        setAccountsLoaded(true);
      })
      .finally(() => setAccountsLoading(false));
  }, [accountsLoaded, accountsLoading, modalOpen, scopeMode]);

  useEffect(() => {
    if (!modalOpen) return;
    if (scopeMode !== 'restricted') return;
    if (!accountsLoaded) return;
    if (!pendingAllowedRefs || pendingAllowedRefs.length === 0) return;

    const allowed = new Set(
      pendingAllowedRefs
        .map((v) => String(v ?? '').trim())
        .filter(Boolean)
    );

    const next = new Set<string>();
    accounts.forEach((item) => {
      const name = String(item?.name ?? '').trim();
      if (!name) return;

      const id = String((item as any)?.id ?? '').trim();
      const authIndexRaw = (item as any)?.authIndex ?? (item as any)?.auth_index ?? (item as any)?.authIndexSnake;
      const authIndex = String(authIndexRaw ?? '').trim();

      if (allowed.has(name) || (id && allowed.has(id)) || (authIndex && allowed.has(authIndex))) {
        next.add(name);
      }
    });

    setSelectedAccounts(next);
    setPendingAllowedRefs(null);
  }, [accounts, accountsLoaded, modalOpen, pendingAllowedRefs, scopeMode]);

  const openAddModal = () => {
    setEditingIndex(null);
    setInputValue('');
    setScopeMode('all');
    setExpiryDays('');
    setExpiryOriginalISO('');
    setExpiryDirty(false);
    setAccountSearch('');
    setSelectedAccounts(new Set());
    setPendingAllowedRefs(null);
    setModalOpen(true);
  };

  const openEditModal = (index: number) => {
    const key = apiKeys[index] ?? '';
    const authEntry = key ? apiKeyAuth[key] : undefined;
    const mode: ScopeMode =
      authEntry === undefined ? 'all' : authEntry.length === 0 ? 'deny' : 'restricted';

    setEditingIndex(index);
    setInputValue(key);
    setScopeMode(mode);
    const currentISO = key ? String(apiKeyExpiry[key] ?? '') : '';
    setExpiryOriginalISO(currentISO);
    setExpiryDirty(false);
    const days = remainingDays(currentISO);
    setExpiryDays(days === null ? '' : String(days));
    setAccountSearch('');
    setSelectedAccounts(new Set());
    setPendingAllowedRefs(mode === 'restricted' && Array.isArray(authEntry) ? authEntry : null);
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    setInputValue('');
    setEditingIndex(null);
    setScopeMode('all');
    setExpiryDays('');
    setExpiryOriginalISO('');
    setExpiryDirty(false);
    setAccountSearch('');
    setSelectedAccounts(new Set());
    setPendingAllowedRefs(null);
  };

  const handleSave = async () => {
    const trimmed = inputValue.trim();
    if (!trimmed) {
      showNotification(`${t('notification.please_enter')} ${t('notification.api_key')}`, 'error');
      return;
    }
    if (!isValidApiKeyCharset(trimmed)) {
      showNotification(t('notification.api_key_invalid_chars'), 'error');
      return;
    }
    if (scopeMode === 'restricted' && selectedAccounts.size === 0) {
      showNotification(t('api_keys.accounts_required'), 'error');
      return;
    }

    const isEdit = editingIndex !== null;
    const oldKey = isEdit && editingIndex !== null ? String(apiKeys[editingIndex] ?? '').trim() : '';
    const nextKeys = isEdit
      ? apiKeys.map((key, idx) => (idx === editingIndex ? trimmed : key))
      : [...apiKeys, trimmed];

    const nextAuth = { ...(apiKeyAuth || {}) };
    if (oldKey && oldKey !== trimmed) {
      delete nextAuth[oldKey];
    }
    if (scopeMode === 'all') {
      delete nextAuth[trimmed];
    } else if (scopeMode === 'deny') {
      nextAuth[trimmed] = [];
    } else {
      nextAuth[trimmed] = Array.from(selectedAccounts).map((v) => String(v).trim()).filter(Boolean);
    }

    const nextExpiry = { ...(apiKeyExpiry || {}) };
    if (oldKey && oldKey !== trimmed) {
      delete nextExpiry[oldKey];
    }
    if (!expiryDirty) {
      // Preserve the original timestamp unless the user explicitly changed it.
      const preserved = isEdit ? String(expiryOriginalISO ?? '') : '';
      if (preserved.trim()) {
        nextExpiry[trimmed] = preserved.trim();
      } else {
        delete nextExpiry[trimmed];
      }
    } else {
      const parsedDays = Number.parseInt(expiryDays.trim(), 10);
      const isoExpiry = expiryISOFromDays(Number.isFinite(parsedDays) ? parsedDays : 0);
      if (!isoExpiry) {
        delete nextExpiry[trimmed];
      } else {
        nextExpiry[trimmed] = isoExpiry;
      }
    }

    setSaving(true);
    try {
      if (isEdit && editingIndex !== null) {
        await apiKeysApi.update(editingIndex, trimmed);
        showNotification(t('notification.api_key_updated'), 'success');
      } else {
        await apiKeysApi.replace(nextKeys);
        showNotification(t('notification.api_key_added'), 'success');
      }

      await Promise.all([
        apiKeysApi.updateAuthMapping(nextAuth),
        apiKeysApi.updateExpiryMapping(nextExpiry),
      ]);

      setApiKeys(nextKeys);
      setApiKeyAuth(nextAuth);
      setApiKeyExpiry(nextExpiry);
      updateConfigValue('api-keys', nextKeys);
      updateConfigValue('api-key-auth', nextAuth);
      updateConfigValue('api-key-expiry', nextExpiry);
      clearCache('api-keys');
      clearCache('api-key-auth');
      clearCache('api-key-expiry');
      closeModal();
    } catch (err: any) {
      showNotification(`${t('notification.update_failed')}: ${err?.message || ''}`, 'error');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = (index: number) => {
    const apiKeyToDelete = apiKeys[index];
    if (!apiKeyToDelete) {
      showNotification(t('notification.delete_failed'), 'error');
      return;
    }

    showConfirmation({
      title: t('common.delete'),
      message: t('api_keys.delete_confirm'),
      variant: 'danger',
      onConfirm: async () => {
        const latestKeys = useConfigStore.getState().config?.apiKeys;
        const currentKeys = Array.isArray(latestKeys) ? latestKeys : [];
        const deleteIndex =
          currentKeys[index] === apiKeyToDelete
            ? index
            : currentKeys.findIndex((key) => key === apiKeyToDelete);

        if (deleteIndex < 0) {
          showNotification(t('notification.delete_failed'), 'error');
          return;
        }

        try {
          await apiKeysApi.delete(deleteIndex);
          const nextKeys = currentKeys.filter((_, idx) => idx !== deleteIndex);

          const nextAuth = { ...(useConfigStore.getState().config?.apiKeyAuth || apiKeyAuth || {}) };
          delete nextAuth[apiKeyToDelete];
          const nextExpiry = { ...(useConfigStore.getState().config?.apiKeyExpiry || apiKeyExpiry || {}) };
          delete nextExpiry[apiKeyToDelete];
          await Promise.all([
            apiKeysApi.updateAuthMapping(nextAuth),
            apiKeysApi.updateExpiryMapping(nextExpiry),
          ]);

          setApiKeys(nextKeys);
          setApiKeyAuth(nextAuth);
          setApiKeyExpiry(nextExpiry);
          updateConfigValue('api-keys', nextKeys);
          updateConfigValue('api-key-auth', nextAuth);
          updateConfigValue('api-key-expiry', nextExpiry);
          clearCache('api-keys');
          clearCache('api-key-auth');
          clearCache('api-key-expiry');
          showNotification(t('notification.api_key_deleted'), 'success');
        } catch (err: any) {
          showNotification(`${t('notification.delete_failed')}: ${err?.message || ''}`, 'error');
        }
      }
    });
  };

  const actionButtons = (
    <div style={{ display: 'flex', gap: 8 }}>
      <Button variant="secondary" size="sm" onClick={() => loadAll(true)} disabled={loading}>
        {t('common.refresh')}
      </Button>
      <Button size="sm" onClick={openAddModal} disabled={disableControls}>
        {t('api_keys.add_button')}
      </Button>
    </div>
  );

  const renderScopeBadge = (key: string) => {
    const entry = apiKeyAuth[key];
    if (entry === undefined) return <span className="pill">{t('api_keys.scope_all')}</span>;
    if (Array.isArray(entry) && entry.length === 0) return <span className="pill">{t('api_keys.scope_deny')}</span>;
    const count = Array.isArray(entry) ? entry.length : 0;
    return <span className="pill">{t('api_keys.scope_restricted')} ({count})</span>;
  };

  const renderExpiryBadge = (key: string) => {
    const raw = apiKeyExpiry[key];
    if (!raw) return <span className="pill">{t('api_keys.expiry_never')}</span>;
    const d = new Date(raw);
    if (Number.isNaN(d.getTime())) return <span className="pill">{t('api_keys.expiry_never')}</span>;
    const expired = d.getTime() <= Date.now();
    if (expired) return <span className="pill">{t('api_keys.expiry_expired')}</span>;
    const days = Math.max(1, Math.ceil((d.getTime() - Date.now()) / dayMs));
    return <span className="pill" title={raw}>{t('api_keys.expiry_in_days', { days })}</span>;
  };

  const filteredAccounts = useMemo(() => {
    const q = accountSearch.trim().toLowerCase();
    if (!q) return accounts;
    return accounts.filter((item) => {
      const id = String(item.id ?? '').toLowerCase();
      const name = String(item.name ?? '').toLowerCase();
      const type = String(item.type ?? item.provider ?? '').toLowerCase();
      const email = String(item.email ?? item.account ?? '').toLowerCase();
      return id.includes(q) || name.includes(q) || type.includes(q) || email.includes(q);
    });
  }, [accountSearch, accounts]);

  const expiryPreview = useMemo(() => {
    if (!expiryDirty) {
      const preserved = String(expiryOriginalISO ?? '').trim();
      if (!preserved) return '';
      const dt = new Date(preserved);
      if (Number.isNaN(dt.getTime())) return '';
      return dt.toLocaleString();
    }

    const d = Number.parseInt(expiryDays.trim(), 10);
    if (!expiryDays.trim() || !Number.isFinite(d) || d <= 0) return '';
    const iso = expiryISOFromDays(d);
    if (!iso) return '';
    const dt = new Date(iso);
    if (Number.isNaN(dt.getTime())) return '';
    return dt.toLocaleString();
  }, [expiryDays, expiryDirty, expiryOriginalISO]);

  return (
    <div className={styles.container}>
      <h1 className={styles.pageTitle}>{t('api_keys.title')}</h1>

      <Card title={t('api_keys.proxy_auth_title')} extra={actionButtons}>
        {error && <div className="error-box">{error}</div>}

        {loading ? (
          <div className="flex-center" style={{ padding: '24px 0' }}>
            <LoadingSpinner size={28} />
          </div>
        ) : apiKeys.length === 0 ? (
          <EmptyState
            title={t('api_keys.empty_title')}
            description={t('api_keys.empty_desc')}
            action={
              <Button onClick={openAddModal} disabled={disableControls}>
                {t('api_keys.add_button')}
              </Button>
            }
          />
        ) : (
          <div className="item-list">
            {apiKeys.map((key, index) => (
              <div key={index} className="item-row">
                <div className="item-meta">
                  <div className="pill">#{index + 1}</div>
                  <div className="item-title">{t('api_keys.item_title')}</div>
                  <div className="item-subtitle">{maskApiKey(String(key || ''))}</div>
                  <div className={styles.metaBadges}>
                    {renderScopeBadge(String(key || '').trim())}
                    {renderExpiryBadge(String(key || '').trim())}
                  </div>
                </div>
                <div className="item-actions">
                  <Button variant="secondary" size="sm" onClick={() => openEditModal(index)} disabled={disableControls}>
                    {t('common.edit')}
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    onClick={() => handleDelete(index)}
                    disabled={disableControls}
                  >
                    {t('common.delete')}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}

        <Modal
          open={modalOpen}
          onClose={closeModal}
          closeDisabled={saving}
          title={editingIndex !== null ? t('api_keys.edit_modal_title') : t('api_keys.add_modal_title')}
        >
          <div className={styles.formGroup}>
            <label htmlFor="apiKeyValue">
              {editingIndex !== null ? t('api_keys.edit_modal_key_label') : t('api_keys.add_modal_key_label')}
            </label>
            <Input
              id="apiKeyValue"
              placeholder={t('api_keys.add_modal_key_placeholder')}
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              disabled={saving}
            />
          </div>

          <div className={styles.formGroup}>
            <label htmlFor="expiryDays">{t('api_keys.expiry_label')}</label>
            <Input
              id="expiryDays"
              type="number"
              value={expiryDays}
              onChange={(e) => {
                setExpiryDirty(true);
                setExpiryDays(e.target.value);
              }}
              placeholder={t('api_keys.expiry_days_placeholder')}
              disabled={saving}
            />
            <div className={styles.hint}>
              {t('api_keys.expiry_hint')}
              {expiryPreview ? ` ${t('api_keys.expiry_preview', { date: expiryPreview })}` : ''}
            </div>
            <div className={styles.presetRow}>
              {[1, 7, 30, 90].map((d) => (
                <Button
                  key={d}
                  type="button"
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    setExpiryDirty(true);
                    setExpiryDays(String(d));
                  }}
                  disabled={saving}
                >
                  {t('api_keys.expiry_preset_days', { days: d })}
                </Button>
              ))}
              <Button
                type="button"
                size="sm"
                variant="secondary"
                onClick={() => {
                  setExpiryDirty(true);
                  setExpiryDays('');
                }}
                disabled={saving}
              >
                {t('api_keys.expiry_never')}
              </Button>
            </div>
          </div>

          <div className={styles.formGroup}>
            <label>{t('api_keys.scope_label')}</label>
            <div className={styles.hint}>{t('api_keys.scope_hint')}</div>
            <div className={styles.scopeRow}>
              <label className={styles.scopeOption}>
                <input
                  type="radio"
                  name="api-key-scope"
                  checked={scopeMode === 'all'}
                  onChange={() => setScopeMode('all')}
                  disabled={saving}
                />
                <span>{t('api_keys.scope_all')}</span>
              </label>
              <label className={styles.scopeOption}>
                <input
                  type="radio"
                  name="api-key-scope"
                  checked={scopeMode === 'restricted'}
                  onChange={() => setScopeMode('restricted')}
                  disabled={saving}
                />
                <span>{t('api_keys.scope_restricted')}</span>
              </label>
              <label className={styles.scopeOption}>
                <input
                  type="radio"
                  name="api-key-scope"
                  checked={scopeMode === 'deny'}
                  onChange={() => setScopeMode('deny')}
                  disabled={saving}
                />
                <span>{t('api_keys.scope_deny')}</span>
              </label>
            </div>
          </div>

          {scopeMode === 'restricted' && (
            <div className={styles.formGroup}>
              <label>{t('api_keys.accounts_label')}</label>
              <div className={styles.accountSearch}>
                <Input
                  value={accountSearch}
                  onChange={(e) => setAccountSearch(e.target.value)}
                  placeholder={t('api_keys.accounts_search_placeholder')}
                  disabled={saving}
                />
              </div>

              <div className={styles.authFilesList}>
                {accountsLoading ? (
                  <div className={styles.emptyAuthFiles}>{t('common.loading')}</div>
                ) : filteredAccounts.length === 0 ? (
                  <div className={styles.emptyAuthFiles}>{t('api_keys.accounts_empty')}</div>
                ) : (
                  filteredAccounts.map((item) => {
                    const name = String(item.name ?? '').trim();
                    if (!name) return null;
                    const checked = selectedAccounts.has(name);
                    const providerType = String(item.type ?? item.provider ?? '').trim();
                    const authIndexRaw = (item as any)?.authIndex ?? (item as any)?.auth_index;
                    const authIndex = String(authIndexRaw ?? '').trim();
                    const email = String((item as any)?.email ?? (item as any)?.account ?? '').trim();

                    return (
                      <div key={name} className={styles.authFileItem}>
                        <input
                          type="checkbox"
                          id={`api-key-auth-${name}`}
                          checked={checked}
                          onChange={() => {
                            setSelectedAccounts((prev) => {
                              const next = new Set(prev);
                              if (next.has(name)) next.delete(name);
                              else next.add(name);
                              return next;
                            });
                          }}
                          disabled={saving}
                        />
                        <label htmlFor={`api-key-auth-${name}`} className={styles.authFileLabel}>
                          <div className={styles.authFileMeta}>
                            <div className={styles.authFileName}>{name}</div>
                            <div className={styles.authFileSub}>
                              {providerType && <span className={styles.authFileType}>{providerType}</span>}
                              {authIndex && <span className={styles.authFileEmail}>#{authIndex}</span>}
                              {email && <span className={styles.authFileEmail}>{email}</span>}
                            </div>
                          </div>
                        </label>
                      </div>
                    );
                  })
                )}
              </div>
            </div>
          )}

          <div className={styles.modalFooter}>
            <Button onClick={closeModal} disabled={saving} variant="secondary">
              {t('common.cancel')}
            </Button>
            <Button onClick={handleSave} disabled={saving} loading={saving}>
              {saving
                ? t('common.saving')
                : editingIndex !== null
                  ? t('common.update')
                  : t('common.add')}
            </Button>
          </div>
        </Modal>
      </Card>
    </div>
  );
}
