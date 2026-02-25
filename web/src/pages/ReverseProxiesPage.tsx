import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { EmptyState } from '@/components/ui/EmptyState';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { useAuthStore, useNotificationStore } from '@/stores';
import { reverseProxiesApi, ReverseProxy, ProxyRoutingAuth, authFilesApi } from '@/services/api';
import type { AuthFileItem } from '@/types/authFile';
import styles from './ReverseProxiesPage.module.scss';

export function ReverseProxiesPage() {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);

  const [proxies, setProxies] = useState<ReverseProxy[]>([]);
  const [routing, setRouting] = useState<ProxyRoutingAuth>({});
  const [authFiles, setAuthFiles] = useState<AuthFileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editingProxy, setEditingProxy] = useState<ReverseProxy | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [proxyToDelete, setProxyToDelete] = useState<ReverseProxy | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [testingProxy, setTestingProxy] = useState<string | null>(null);
  const [filterStatus, setFilterStatus] = useState<'all' | 'enabled' | 'disabled'>('all');
  const [accountQuery, setAccountQuery] = useState('');
  const [workerUrl, setWorkerUrl] = useState('');
  const [savingWorker, setSavingWorker] = useState(false);

  const [formData, setFormData] = useState({
    name: '',
    baseUrl: '',
    description: '',
    enabled: true,
    timeout: 30,
    headers: '',
    authAccounts: [] as string[]
  });

  const disableControls = useMemo(() => connectionStatus !== 'connected', [connectionStatus]);

  const proxyNameMap = useMemo(() => {
    const map: Record<string, string> = {};
    proxies.forEach(proxy => {
      map[proxy.id] = proxy.name;
    });
    return map;
  }, [proxies]);

  const normalizeKey = (value: unknown) => String(value == null ? '' : value).trim();

  const pathBaseName = (value: string) => {
    const normalized = value.replace(/\\/g, '/');
    const segments = normalized.split('/').filter(Boolean);
    return segments.length > 0 ? segments[segments.length - 1] : '';
  };

  const getAuthKeyCandidates = (authFile: AuthFileItem) => {
    const candidates: string[] = [];
    const seen = new Set<string>();
    const add = (raw: unknown) => {
      const key = normalizeKey(raw);
      if (!key || seen.has(key)) return;
      seen.add(key);
      candidates.push(key);
    };

    add(authFile.id);
    add(authFile.auth_index);
    add(authFile.authIndex);
    add(authFile.name);

    const path = normalizeKey((authFile as any).path);
    if (path) {
      add(path);
      add(pathBaseName(path));
    }

    return candidates;
  };

  const getAuthPrimaryKey = (authFile: AuthFileItem) => {
    return getAuthKeyCandidates(authFile)[0] || '';
  };

  const getAssignedProxyId = (authFile: AuthFileItem, routingMap: ProxyRoutingAuth) => {
    const keys = getAuthKeyCandidates(authFile);
    for (const key of keys) {
      if (routingMap[key]) {
        return routingMap[key];
      }
    }
    const normalizedKeySet = new Set(keys.map((key) => key.toLowerCase()));
    for (const [routingKey, proxyId] of Object.entries(routingMap)) {
      if (normalizedKeySet.has(routingKey.toLowerCase())) {
        return proxyId;
      }
    }
    return '';
  };

  const clearAuthRouting = (routingMap: ProxyRoutingAuth, authFile: AuthFileItem, proxyId?: string) => {
    const keys = getAuthKeyCandidates(authFile);
    const normalizedKeySet = new Set(keys.map((key) => key.toLowerCase()));
    Object.keys(routingMap).forEach((routingKey) => {
      if (!normalizedKeySet.has(routingKey.toLowerCase())) return;
      if (!proxyId || routingMap[routingKey] === proxyId) {
        delete routingMap[routingKey];
      }
    });
  };

  const getAuthDisplayName = (authFile: AuthFileItem) => {
    const raw =
      authFile.label ||
      authFile.email ||
      authFile.account ||
      authFile.name ||
      getAuthPrimaryKey(authFile);
    return String(raw || t('common.not_set'));
  };

  const getAuthProvider = (authFile: AuthFileItem) => {
    const raw = authFile.type || authFile.provider || 'unknown';
    return String(raw || 'unknown');
  };

  const knownAuthKeys = useMemo(() => {
    const keys = new Set<string>();
    authFiles.forEach((authFile) => {
      getAuthKeyCandidates(authFile).forEach((key) => {
        keys.add(key);
        keys.add(key.toLowerCase());
      });
    });
    return keys;
  }, [authFiles]);

  const filteredAuthFiles = useMemo(() => {
    if (!accountQuery.trim()) {
      return authFiles;
    }
    const query = accountQuery.trim().toLowerCase();
    return authFiles.filter((authFile) => {
      const name = getAuthDisplayName(authFile).toLowerCase();
      const provider = getAuthProvider(authFile).toLowerCase();
      const email = String(authFile.email || '').toLowerCase();
      return name.includes(query) || provider.includes(query) || email.includes(query);
    });
  }, [authFiles, accountQuery, t]);

  // 过滤和搜索
  const filteredProxies = useMemo(() => {
    return proxies.filter(proxy => {
      // 状态过滤
      if (filterStatus === 'enabled' && !proxy.enabled) return false;
      if (filterStatus === 'disabled' && proxy.enabled) return false;

      // 搜索过滤
      if (searchQuery) {
        const query = searchQuery.toLowerCase();
        return (
          proxy.name.toLowerCase().includes(query) ||
          proxy['base-url'].toLowerCase().includes(query) ||
          proxy.description?.toLowerCase().includes(query)
        );
      }

      return true;
    });
  }, [proxies, searchQuery, filterStatus]);

  const loadProxies = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [proxiesData, routingData, authFilesData, workerUrlData] = await Promise.all([
        reverseProxiesApi.list(),
        reverseProxiesApi.getRoutingAuth(),
        authFilesApi.list(),
        reverseProxiesApi.getWorkerUrl()
      ]);
      setProxies(proxiesData);
      setRouting(routingData);
      setAuthFiles(authFilesData.files || []);
      setWorkerUrl(workerUrlData || '');
    } catch (err: any) {
      setError(err?.message || t('reverseProxies.loadError'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadProxies();
  }, [loadProxies]);

  const openAddModal = () => {
    setEditingProxy(null);
    setAccountQuery('');
    setFormData({
      name: '',
      baseUrl: '',
      description: '',
      enabled: true,
      timeout: 30,
      headers: '',
      authAccounts: []
    });
    setModalOpen(true);
  };

  const openEditModal = (proxy: ReverseProxy) => {
    setEditingProxy(proxy);
    setAccountQuery('');
    const assignedAuthKeys = authFiles
      .filter(authFile => getAssignedProxyId(authFile, routing) === proxy.id)
      .map(authFile => getAuthPrimaryKey(authFile))
      .filter(Boolean);

    setFormData({
      name: proxy.name,
      baseUrl: proxy['base-url'],
      description: proxy.description || '',
      enabled: proxy.enabled ?? true,
      timeout: proxy.timeout || 30,
      headers: proxy.headers ? JSON.stringify(proxy.headers, null, 2) : '',
      authAccounts: assignedAuthKeys
    });
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    setEditingProxy(null);
  };

  const handleSave = async () => {
    if (!formData.name.trim()) {
      showNotification(t('reverseProxies.nameRequired'), 'error');
      return;
    }
    if (!formData.baseUrl.trim()) {
      showNotification(t('reverseProxies.baseUrlRequired'), 'error');
      return;
    }

    let headers: Record<string, string> | undefined;
    if (formData.headers.trim()) {
      try {
        headers = JSON.parse(formData.headers);
      } catch (e) {
        showNotification(t('reverseProxies.invalidHeaders'), 'error');
        return;
      }
    }

    const proxyData: any = {
      name: formData.name.trim(),
      'base-url': formData.baseUrl.trim(),
      enabled: formData.enabled,
    };

    if (formData.description.trim()) {
      proxyData.description = formData.description.trim();
    }
    if (formData.timeout) {
      proxyData.timeout = formData.timeout;
    }
    if (headers) {
      proxyData.headers = headers;
    }

    setSaving(true);
    try {
      let proxyId: string;
      if (editingProxy) {
        await reverseProxiesApi.update(editingProxy.id, proxyData);
        proxyId = editingProxy.id;
        showNotification(t('reverseProxies.updateSuccess'), 'success');
      } else {
        const created = await reverseProxiesApi.create(proxyData);
        proxyId = created.id;
        showNotification(t('reverseProxies.createSuccess'), 'success');
      }

      // 更新 auth-level routing
      const newRouting: ProxyRoutingAuth = { ...routing };
      const selectedAccounts = new Set(formData.authAccounts);

      authFiles.forEach(authFile => {
        const primaryKey = getAuthPrimaryKey(authFile);
        if (!primaryKey) return;

        const assignedProxyId = getAssignedProxyId(authFile, routing);
        const shouldAssign = selectedAccounts.has(primaryKey);

        if (shouldAssign) {
          clearAuthRouting(newRouting, authFile);
          newRouting[primaryKey] = proxyId;
          return;
        }

        if (assignedProxyId === proxyId) {
          clearAuthRouting(newRouting, authFile, proxyId);
        }
      });

      await reverseProxiesApi.updateRoutingAuth(newRouting);

      closeModal();
      await loadProxies();
    } catch (err: any) {
      showNotification(err?.message || t('reverseProxies.saveError'), 'error');
    } finally {
      setSaving(false);
    }
  };

  const openDeleteConfirm = (proxy: ReverseProxy) => {
    setProxyToDelete(proxy);
    setDeleteConfirmOpen(true);
  };

  const handleDelete = async () => {
    if (!proxyToDelete) return;

    try {
      await reverseProxiesApi.delete(proxyToDelete.id);
      showNotification(t('reverseProxies.deleteSuccess'), 'success');
      setDeleteConfirmOpen(false);
      setProxyToDelete(null);
      await loadProxies();
    } catch (err: any) {
      showNotification(err?.message || t('reverseProxies.deleteError'), 'error');
    }
  };

  const handleTestProxy = async (proxy: ReverseProxy) => {
    setTestingProxy(proxy.id);
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 10000);

      // 尝试多种方法测试连接
      let testMethod = '';

      // 方法1: 尝试 HEAD 请求
      try {
        await fetch(proxy['base-url'], {
          method: 'HEAD',
          signal: controller.signal,
          mode: 'no-cors', // 避免 CORS 问题
        });
        testMethod = 'HEAD';
      } catch {
        // 方法2: 如果 HEAD 失败，尝试 GET 请求
        await fetch(proxy['base-url'], {
          method: 'GET',
          signal: controller.signal,
          mode: 'no-cors',
        });
        testMethod = 'GET';
      }

      clearTimeout(timeoutId);

      // no-cors 模式下，响应类型是 'opaque'，无法读取状态码
      // 只要没有抛出异常，就认为连接成功
      showNotification(
        t('reverseProxies.testSuccess', { name: proxy.name }) + ` (${testMethod})`,
        'success'
      );
    } catch (err: any) {
      if (err.name === 'AbortError') {
        showNotification(
          t('reverseProxies.testTimeout', { name: proxy.name }) + ' (10s)',
          'warning'
        );
      } else {
        const errorMsg = err.message || err.toString();
        showNotification(
          t('reverseProxies.testFailed', { name: proxy.name }) + `: ${errorMsg}`,
          'error'
        );
      }
    } finally {
      setTestingProxy(null);
    }
  };

  const handleCopyConfig = (proxy: ReverseProxy) => {
    const config = {
      name: proxy.name,
      'base-url': proxy['base-url'],
      enabled: proxy.enabled,
      description: proxy.description,
      timeout: proxy.timeout,
      headers: proxy.headers,
    };

    navigator.clipboard.writeText(JSON.stringify(config, null, 2))
      .then(() => showNotification(t('reverseProxies.configCopied'), 'success'))
      .catch(() => showNotification(t('reverseProxies.copyFailed'), 'error'));
  };

  const handleToggleEnabled = async (proxy: ReverseProxy) => {
    try {
      await reverseProxiesApi.update(proxy.id, {
        ...proxy,
        enabled: !proxy.enabled,
      });
      showNotification(
        proxy.enabled ? t('reverseProxies.disableSuccess') : t('reverseProxies.enableSuccess'),
        'success'
      );
      await loadProxies();
    } catch (err: any) {
      showNotification(err?.message || t('reverseProxies.toggleError'), 'error');
    }
  };

  const handleSaveWorkerConfig = async () => {
    const value = workerUrl.trim();
    if (value) {
      try {
        const parsed = new URL(value);
        if (!['http:', 'https:'].includes(parsed.protocol)) {
          showNotification(t('reverseProxies.workerUrlInvalid'), 'error');
          return;
        }
      } catch {
        showNotification(t('reverseProxies.workerUrlInvalid'), 'error');
        return;
      }
    }

    setSavingWorker(true);
    try {
      const saved = await reverseProxiesApi.updateWorkerUrl(value);
      setWorkerUrl(saved || '');
      showNotification(t('reverseProxies.workerUrlSaved'), 'success');
    } catch (err: any) {
      showNotification(err?.message || t('reverseProxies.workerUrlSaveError'), 'error');
    } finally {
      setSavingWorker(false);
    }
  };

  return (
    <div className={styles.container}>
      <h1 className={styles.pageTitle}>{t('reverseProxies.title')}</h1>

      <div className={styles.content}>
        <div className={styles.header}>
          <div className={styles.headerLeft}>
            <p className={styles.subtitle}>{t('reverseProxies.subtitle')}</p>
          </div>
          <div className={styles.actions}>
            <Button onClick={loadProxies} disabled={disableControls}>
              {t('common.refresh')}
            </Button>
            <Button onClick={openAddModal} disabled={disableControls} variant="primary">
              {t('reverseProxies.addProxy')}
            </Button>
          </div>
        </div>

        <Card className={styles.workerConfigCard}>
          <div className={styles.workerConfigTitle}>{t('reverseProxies.workerConfigTitle')}</div>
          <div className={styles.workerConfigSubtitle}>{t('reverseProxies.workerConfigSubtitle')}</div>
          <div className={styles.workerConfigRow}>
            <Input
              value={workerUrl}
              onChange={(e) => setWorkerUrl(e.target.value)}
              placeholder="https://cpa-deno-bridge.mengcenfay.workers.dev"
              disabled={disableControls || savingWorker}
            />
            <Button onClick={handleSaveWorkerConfig} disabled={disableControls || savingWorker} loading={savingWorker}>
              {t('common.save')}
            </Button>
          </div>
          <div className={styles.workerConfigHint}>{t('reverseProxies.workerConfigHint')}</div>
          <div className={styles.workerConfigExample}>
            {t('reverseProxies.workerConfigExample')}
          </div>
        </Card>

        {/* 搜索和过滤栏 */}
        {proxies.length > 0 && (
          <div className={styles.filterBar}>
            <div className={styles.searchBox}>
              <Input
                placeholder={t('reverseProxies.searchPlaceholder', { defaultValue: '搜索代理名称、URL 或描述...' })}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
            </div>
            <div className={styles.filterButtons}>
              <Button
                size="sm"
                variant={filterStatus === 'all' ? 'primary' : 'secondary'}
                onClick={() => setFilterStatus('all')}
              >
                {t('reverseProxies.filterAll', { defaultValue: '全部' })} ({proxies.length})
              </Button>
              <Button
                size="sm"
                variant={filterStatus === 'enabled' ? 'primary' : 'secondary'}
                onClick={() => setFilterStatus('enabled')}
              >
                {t('common.enabled')} ({proxies.filter(p => p.enabled).length})
              </Button>
              <Button
                size="sm"
                variant={filterStatus === 'disabled' ? 'primary' : 'secondary'}
                onClick={() => setFilterStatus('disabled')}
              >
                {t('common.disabled')} ({proxies.filter(p => !p.enabled).length})
              </Button>
            </div>
          </div>
        )}

        {loading && <LoadingSpinner />}

        {error && (
          <Card>
            <p style={{ color: 'var(--error-color)' }}>{error}</p>
          </Card>
        )}

        {!loading && !error && proxies.length === 0 && (
          <EmptyState
            title={t('reverseProxies.noProxies')}
            description={t('reverseProxies.noProxiesDesc')}
            action={<Button onClick={openAddModal}>{t('reverseProxies.addProxy')}</Button>}
          />
        )}

        {!loading && !error && filteredProxies.length === 0 && proxies.length > 0 && (
          <EmptyState
            title={t('reverseProxies.noResults', { defaultValue: '未找到匹配的代理' })}
            description={t('reverseProxies.noResultsDesc', { defaultValue: '尝试调整搜索条件或过滤器' })}
          />
        )}

        {!loading && !error && filteredProxies.length > 0 && (
          <div className={styles.proxyList}>
            {filteredProxies.map((proxy) => {
              const assignedAccounts = authFiles.filter(
                (authFile) => getAssignedProxyId(authFile, routing) === proxy.id
              );
              const unknownAssignments = Object.entries(routing)
                .filter(([key, id]) => id === proxy.id && !knownAuthKeys.has(key) && !knownAuthKeys.has(key.toLowerCase()))
                .map(([key]) => key);

              return (
                <Card key={proxy.id} className={styles.proxyCard}>
                  <div className={styles.cardHeader}>
                    <div className={styles.proxyInfo}>
                      <div className={styles.proxyName}>
                        {proxy.name}
                        <span className={`${styles.statusBadge} ${proxy.enabled ? styles.enabled : styles.disabled}`}>
                          {proxy.enabled ? t('common.enabled') : t('common.disabled')}
                        </span>
                      </div>
                      <div className={styles.proxyUrl}>{proxy['base-url']}</div>
                      {proxy.description && (
                        <div className={styles.proxyDesc}>{proxy.description}</div>
                      )}
                    </div>
                    <div className={styles.cardActions}>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => handleToggleEnabled(proxy)}
                        disabled={disableControls}
                        title={proxy.enabled ? t('reverseProxies.disable') : t('reverseProxies.enable')}
                      >
                        {proxy.enabled ? '⏸' : '▶'}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => handleTestProxy(proxy)}
                        disabled={disableControls}
                        loading={testingProxy === proxy.id}
                        title={t('reverseProxies.testConnection', { defaultValue: '测试连接' })}
                      >
                        🔍
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => handleCopyConfig(proxy)}
                        disabled={disableControls}
                        title={t('reverseProxies.copyConfig', { defaultValue: '复制配置' })}
                      >
                        📋
                      </Button>
                      <Button size="sm" onClick={() => openEditModal(proxy)} disabled={disableControls}>
                        {t('common.edit')}
                      </Button>
                      <Button
                        size="sm"
                        variant="danger"
                        onClick={() => openDeleteConfirm(proxy)}
                        disabled={disableControls}
                      >
                        {t('common.delete')}
                      </Button>
                    </div>
                  </div>

                  <div className={styles.cardBody}>
                    <div className={styles.field}>
                        <div className={styles.label}>{t('reverseProxies.usedBy')}</div>
                        <div className={styles.value}>
                        {assignedAccounts.length > 0 || unknownAssignments.length > 0 ? (
                          <div className={styles.accountTags}>
                            {assignedAccounts.map(account => {
                              const accountKey = getAuthPrimaryKey(account);
                              const displayName = getAuthDisplayName(account);
                              const providerType = getAuthProvider(account);
                              return (
                                <span key={accountKey} className={styles.accountTag}>
                                  <span className={styles.accountTagName}>{displayName}</span>
                                  <span className={styles.accountTagType}>{providerType}</span>
                                </span>
                              );
                            })}
                            {unknownAssignments.length > 0 && (
                              <span className={styles.accountTagMuted}>
                                {t('reverseProxies.unknownAssignments', { count: unknownAssignments.length })}
                              </span>
                            )}
                          </div>
                        ) : (
                          <span className={styles.notUsed}>{t('reverseProxies.notUsed')}</span>
                        )}
                      </div>
                    </div>
                    {proxy.timeout && (
                      <div className={styles.field}>
                        <div className={styles.label}>{t('reverseProxies.timeout')}</div>
                        <div className={styles.value}>{proxy.timeout}s</div>
                      </div>
                    )}
                    {proxy.headers && Object.keys(proxy.headers).length > 0 && (
                      <div className={styles.field}>
                        <div className={styles.label}>{t('reverseProxies.customHeaders')}</div>
                        <div className={styles.value}>{Object.keys(proxy.headers).length} {t('reverseProxies.headers')}</div>
                      </div>
                    )}
                    {proxy['created-at'] && (
                      <div className={styles.field}>
                        <div className={styles.label}>{t('reverseProxies.createdAt', { defaultValue: '创建时间' })}</div>
                        <div className={styles.value}>
                          {new Date(proxy['created-at']).toLocaleString()}
                        </div>
                      </div>
                    )}
                  </div>
                </Card>
              );
            })}
          </div>
        )}
      </div>

      {/* 添加/编辑模态框 */}
      <Modal
        open={modalOpen}
        onClose={closeModal}
        title={editingProxy ? t('reverseProxies.editProxy') : t('reverseProxies.addProxy')}
      >
        <div className={styles.formGroup}>
          <label htmlFor="name">{t('reverseProxies.name')} *</label>
          <Input
            id="name"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder={t('reverseProxies.namePlaceholder')}
          />
        </div>

        <div className={styles.formGroup}>
          <label htmlFor="baseUrl">{t('reverseProxies.baseUrl')} *</label>
          <Input
            id="baseUrl"
            value={formData.baseUrl}
            onChange={(e) => setFormData({ ...formData, baseUrl: e.target.value })}
            placeholder="https://d1.api.augmentcode.com"
          />
          <div className={styles.hint}>{t('reverseProxies.baseUrlHint')}</div>
        </div>

        <div className={styles.formGroup}>
          <label htmlFor="description">{t('reverseProxies.description')}</label>
          <Input
            id="description"
            value={formData.description}
            onChange={(e) => setFormData({ ...formData, description: e.target.value })}
            placeholder={t('reverseProxies.descriptionPlaceholder')}
          />
        </div>

        <div className={styles.formGroup}>
          <label htmlFor="timeout">{t('reverseProxies.timeout')} (s)</label>
          <Input
            id="timeout"
            type="number"
            value={formData.timeout}
            onChange={(e) => setFormData({ ...formData, timeout: parseInt(e.target.value) || 30 })}
            placeholder="30"
          />
        </div>

        <div className={styles.formGroup}>
          <div className={styles.checkbox}>
            <input
              type="checkbox"
              id="enabled"
              checked={formData.enabled}
              onChange={(e) => setFormData({ ...formData, enabled: e.target.checked })}
            />
            <label htmlFor="enabled">{t('common.enabled')}</label>
          </div>
        </div>

        <div className={styles.formGroup}>
          <label htmlFor="headers">{t('reverseProxies.customHeaders')} (JSON)</label>
          <textarea
            id="headers"
            value={formData.headers}
            onChange={(e) => setFormData({ ...formData, headers: e.target.value })}
            placeholder='{"Authorization": "Bearer token"}'
          />
          <div className={styles.hint}>{t('reverseProxies.headersHint')}</div>
        </div>

          <div className={styles.formGroup}>
            <label>{t('reverseProxies.applyTo')}</label>
            <div className={styles.hint} style={{ marginBottom: '0.75rem' }}>
              {t('reverseProxies.applyToHint')}
            </div>
            <div className={styles.accountSearch}>
              <Input
                value={accountQuery}
                onChange={(e) => setAccountQuery(e.target.value)}
                placeholder={t('reverseProxies.accountSearchPlaceholder')}
              />
            </div>
            <div className={styles.authFilesList}>
              {authFiles.length === 0 ? (
                <div className={styles.emptyAuthFiles}>
                  {t('reverseProxies.noAuthFiles')}
                </div>
              ) : filteredAuthFiles.length === 0 ? (
                <div className={styles.emptyAuthFiles}>
                  {t('reverseProxies.noAuthMatches')}
                </div>
              ) : (
                filteredAuthFiles.map(authFile => {
                  const authId = getAuthPrimaryKey(authFile);
                  const displayName = getAuthDisplayName(authFile);
                  const providerType = getAuthProvider(authFile);
                  const assignedProxyId = getAssignedProxyId(authFile, routing);
                  const assignedProxyName = assignedProxyId ? (proxyNameMap[assignedProxyId] || assignedProxyId) : '';
                  const isSelected = formData.authAccounts.includes(authId);
                  const currentProxyId = editingProxy?.id || '';
                  const isAssignedHere = currentProxyId !== '' && assignedProxyId === currentProxyId;
                  const assignedElsewhere = Boolean(assignedProxyId) && assignedProxyId !== currentProxyId;
                  const willMove = isSelected && assignedElsewhere;
                  const willAssign = isSelected && !assignedProxyId;
                  const willUnassign = !isSelected && isAssignedHere;

                  let statusText = t('reverseProxies.unassigned');
                  let statusClass = styles.assignmentBadgeMuted;

                  if (isSelected) {
                    if (willMove) {
                      statusText = t('reverseProxies.willMove', { name: assignedProxyName });
                      statusClass = styles.assignmentBadgeMove;
                    } else if (isAssignedHere) {
                      statusText = t('reverseProxies.assignedHere');
                      statusClass = styles.assignmentBadgeAssigned;
                    } else if (willAssign) {
                      statusText = t('reverseProxies.willAssign');
                      statusClass = styles.assignmentBadgePending;
                    }
                  } else if (willUnassign) {
                    statusText = t('reverseProxies.willUnassign');
                    statusClass = styles.assignmentBadgePending;
                  } else if (assignedElsewhere) {
                    statusText = t('reverseProxies.assignedTo', { name: assignedProxyName });
                    statusClass = styles.assignmentBadgeAssigned;
                  }

                  return (
                    <div key={authId} className={styles.authFileItem}>
                      <input
                        type="checkbox"
                        id={`auth-${authId}`}
                        checked={isSelected}
                        onChange={(e) => {
                          if (e.target.checked) {
                            setFormData({ ...formData, authAccounts: [...formData.authAccounts, authId] });
                          } else {
                            setFormData({ ...formData, authAccounts: formData.authAccounts.filter(p => p !== authId) });
                          }
                        }}
                      />
                      <label htmlFor={`auth-${authId}`} className={styles.authFileLabel}>
                        <div className={styles.authFileMeta}>
                          <div className={styles.authFileName}>{displayName}</div>
                          <div className={styles.authFileSub}>
                            <span className={styles.authFileType}>{providerType}</span>
                            {authFile.email && (
                              <span className={styles.authFileEmail}>{authFile.email}</span>
                            )}
                          </div>
                        </div>
                        <div className={styles.authFileStatus}>
                          <span className={`${styles.assignmentBadge} ${statusClass}`}>
                            {statusText}
                          </span>
                        </div>
                      </label>
                    </div>
                  );
                })
              )}
            </div>
          </div>

        <div className={styles.modalFooter}>
          <Button onClick={closeModal} disabled={saving} variant="secondary">
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSave} variant="primary" disabled={saving}>
            {saving ? t('common.saving') : t('common.save')}
          </Button>
        </div>
      </Modal>

      {/* 删除确认模态框 */}
      <Modal
        open={deleteConfirmOpen}
        onClose={() => setDeleteConfirmOpen(false)}
        title={t('common.confirmDelete')}
      >
        <p>{t('reverseProxies.deleteConfirm', { name: proxyToDelete?.name })}</p>
        <p style={{ color: 'var(--text-secondary)', marginTop: '0.5rem' }}>
          {t('common.cannotUndo')}
        </p>
        <div className={styles.modalFooter}>
          <Button onClick={() => setDeleteConfirmOpen(false)} variant="secondary">
            {t('common.cancel')}
          </Button>
          <Button onClick={handleDelete} variant="danger">
            {t('common.delete')}
          </Button>
        </div>
      </Modal>
    </div>
  );
}
