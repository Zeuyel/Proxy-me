import { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useInterval } from '@/hooks/useInterval';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Input } from '@/components/ui/Input';
import { AutocompleteInput } from '@/components/ui/AutocompleteInput';
import { Modal } from '@/components/ui/Modal';
import { EmptyState } from '@/components/ui/EmptyState';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  ANTIGRAVITY_CONFIG,
  CODEX_CONFIG,
  GEMINI_CLI_CONFIG,
} from '@/components/quota';
import { useQuotaLoader } from '@/components/quota/useQuotaLoader';
import type { QuotaStatusState } from '@/components/quota/QuotaCard';
import {
  IconBot,
  IconChevronDown,
  IconClipboard,
  IconDownload,
  IconFileText,
  IconInfo,
  IconRefreshCw,
  IconTrash2,
  IconUpload,
  IconX,
} from '@/components/ui/icons';
import { useAuthStore, useNotificationStore, useThemeStore } from '@/stores';
import { authFilesApi, usageApi } from '@/services/api';
import { apiKeysApi } from '@/services/api/apiKeys';
import { apiClient } from '@/services/api/client';
import type {
  AntigravityQuotaState,
  AuthFileItem,
  CodexQuotaState,
  GeminiCliQuotaState,
  OAuthModelMappingEntry,
} from '@/types';
import { isAntigravityFile, isCodexFile, isGeminiCliFile } from '@/utils/quota';
import {
  calculateStatusBarData,
  collectUsageDetails,
  normalizeUsageSourceId,
  type KeyStatBucket,
  type KeyStats,
  type UsageDetail,
} from '@/utils/usage';
import { formatFileSize } from '@/utils/format';
import { generateId } from '@/utils/helpers';
import styles from './AuthFilesPage.module.scss';

type ThemeColors = { bg: string; text: string; border?: string };
type TypeColorSet = { light: ThemeColors; dark?: ThemeColors };
type ResolvedTheme = 'light' | 'dark';
type AuthFileModelItem = { id: string; display_name?: string; type?: string; owned_by?: string };

// 标签类型颜色配置（对齐重构前 styles.css 的 file-type-badge 颜色）
const TYPE_COLORS: Record<string, TypeColorSet> = {
  qwen: {
    light: { bg: '#e8f5e9', text: '#2e7d32' },
    dark: { bg: '#1b5e20', text: '#81c784' },
  },
  gemini: {
    light: { bg: '#e3f2fd', text: '#1565c0' },
    dark: { bg: '#0d47a1', text: '#64b5f6' },
  },
  'gemini-cli': {
    light: { bg: '#e7efff', text: '#1e4fa3' },
    dark: { bg: '#1c3f73', text: '#a8c7ff' },
  },
  aistudio: {
    light: { bg: '#f0f2f5', text: '#2f343c' },
    dark: { bg: '#373c42', text: '#cfd3db' },
  },
  claude: {
    light: { bg: '#fce4ec', text: '#c2185b' },
    dark: { bg: '#880e4f', text: '#f48fb1' },
  },
  codex: {
    light: { bg: '#fff3e0', text: '#ef6c00' },
    dark: { bg: '#e65100', text: '#ffb74d' },
  },
  antigravity: {
    light: { bg: '#e0f7fa', text: '#006064' },
    dark: { bg: '#004d40', text: '#80deea' },
  },
  iflow: {
    light: { bg: '#f3e5f5', text: '#7b1fa2' },
    dark: { bg: '#4a148c', text: '#ce93d8' },
  },
  empty: {
    light: { bg: '#f5f5f5', text: '#616161' },
    dark: { bg: '#424242', text: '#bdbdbd' },
  },
  unknown: {
    light: { bg: '#f0f0f0', text: '#666666', border: '1px dashed #999999' },
    dark: { bg: '#3a3a3a', text: '#aaaaaa', border: '1px dashed #666666' },
  },
};

const OAUTH_PROVIDER_PRESETS = [
  'gemini-cli',
  'vertex',
  'aistudio',
  'antigravity',
  'claude',
  'codex',
  'qwen',
  'iflow',
];

const OAUTH_PROVIDER_EXCLUDES = new Set(['all', 'unknown', 'empty']);
const MAX_AUTH_FILE_SIZE = 50 * 1024;

interface ExcludedFormState {
  provider: string;
  modelsText: string;
}

type OAuthModelMappingFormEntry = OAuthModelMappingEntry & { id: string };

interface ModelMappingsFormState {
  provider: string;
  mappings: OAuthModelMappingFormEntry[];
}

interface AuthFileMetadataEditorState {
  originalName: string;
  fileName: string;
  displayName: string;
  tagsText: string;
  loadingFile: boolean;
  fileError: string | null;
  originalJsonText: string;
  jsonText: string;
  json: Record<string, unknown> | null;
  saving: boolean;
}

const buildEmptyMappingEntry = (): OAuthModelMappingFormEntry => ({
  id: generateId(),
  name: '',
  alias: '',
  fork: false,
});
// 标准化 auth_index 值（与 usage.ts 中的 normalizeAuthIndex 保持一致）
function normalizeAuthIndexValue(value: unknown): string | null {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value.toString();
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    return trimmed ? trimmed : null;
  }
  return null;
}

function isRuntimeOnlyAuthFile(file: AuthFileItem): boolean {
  const raw = file['runtime_only'] ?? file.runtimeOnly;
  if (typeof raw === 'boolean') return raw;
  if (typeof raw === 'string') return raw.trim().toLowerCase() === 'true';
  return false;
}

function parseTimestampValue(raw: unknown): Date | null {
  if (!raw) return null;
  const asNumber = Number(raw);
  const date =
    Number.isFinite(asNumber) && !Number.isNaN(asNumber)
      ? new Date(asNumber < 1e12 ? asNumber * 1000 : asNumber)
      : new Date(String(raw));
  return Number.isNaN(date.getTime()) ? null : date;
}

function isEffectiveDisabled(file: AuthFileItem): boolean {
  const raw = file['disabled_effective'] ?? file.disabledEffective;
  if (typeof raw === 'boolean') return raw || file.disabled === true;
  if (typeof raw === 'string') return raw.trim().toLowerCase() === 'true' || file.disabled === true;
  return file.disabled === true;
}

function isCooldownDisabled(file: AuthFileItem): boolean {
  const raw = file['cooldown_active'] ?? file.cooldownActive;
  if (typeof raw === 'boolean') return raw;
  if (typeof raw === 'string') return raw.trim().toLowerCase() === 'true';
  return false;
}

const CODEX_QUOTA_USED_PERCENT_EXHAUSTED_THRESHOLD = 99.5;

function isQuotaTokenInvalidated401(status: number | undefined, message: string | undefined): boolean {
  if (status === 401) return true;
  const normalized = String(message ?? '')
    .trim()
    .toLowerCase();
  if (!normalized) return false;
  const has401 = normalized.includes('401');
  const hasTokenInvalidated =
    normalized.includes('token has been invalidated') ||
    normalized.includes('authentication token has been invalidated');
  const hasSignInAgain = normalized.includes('sign in again') || normalized.includes('signing in again');
  return has401 && (hasTokenInvalidated || hasSignInAgain);
}

function hasPositiveNumber(value: unknown): boolean {
  const num = Number(value);
  return Number.isFinite(num) && num > 0;
}

function normalizeAuthFileUsageTotal(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string') {
    const parsed = Number(value.trim());
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

function statusBarDataFromAuthFileRecentRequests(
  buckets: unknown
): ReturnType<typeof calculateStatusBarData> | null {
  if (!Array.isArray(buckets)) return null;
  const normalized = buckets.slice(-20).map((item) => {
    const record = item && typeof item === 'object' ? (item as Record<string, unknown>) : {};
    return {
      success: normalizeAuthFileUsageTotal(record.success),
      failure: normalizeAuthFileUsageTotal(record.failed),
    };
  });
  if (normalized.length === 0) return null;

  const emptyCount = Math.max(0, 20 - normalized.length);
  const blockStats = [
    ...Array.from({ length: emptyCount }, () => ({ success: 0, failure: 0 })),
    ...normalized,
  ];
  let totalSuccess = 0;
  let totalFailure = 0;
  const blocks = blockStats.map((stat) => {
    totalSuccess += stat.success;
    totalFailure += stat.failure;
    if (stat.success === 0 && stat.failure === 0) return 'idle';
    if (stat.failure === 0) return 'success';
    if (stat.success === 0) return 'failure';
    return 'mixed';
  });
  const total = totalSuccess + totalFailure;
  return {
    blocks,
    successRate: total > 0 ? (totalSuccess / total) * 100 : 100,
    totalSuccess,
    totalFailure,
  };
}

// 解析认证文件的统计数据
function resolveAuthFileStats(file: AuthFileItem, stats: KeyStats): KeyStatBucket {
  const defaultStats: KeyStatBucket = { success: 0, failure: 0 };
  const rawFileName = file?.name || '';

  // 兼容 auth_index 和 authIndex 两种字段名（API 返回的是 auth_index）
  const rawAuthIndex = file['auth_index'] ?? file.authIndex;
  const authIndexKey = normalizeAuthIndexValue(rawAuthIndex);

  // 尝试根据 authIndex 匹配
  if (authIndexKey && stats.byAuthIndex?.[authIndexKey]) {
    return stats.byAuthIndex[authIndexKey];
  }

  // 尝试根据 source (文件名) 匹配
  const fileNameId = rawFileName ? normalizeUsageSourceId(rawFileName) : '';
  if (fileNameId && stats.bySource?.[fileNameId]) {
    const fromName = stats.bySource[fileNameId];
    if (fromName.success > 0 || fromName.failure > 0) {
      return fromName;
    }
  }

  // 尝试去掉扩展名后匹配
  if (rawFileName) {
    const nameWithoutExt = rawFileName.replace(/\.[^/.]+$/, '');
    if (nameWithoutExt && nameWithoutExt !== rawFileName) {
      const nameWithoutExtId = normalizeUsageSourceId(nameWithoutExt);
      const fromNameWithoutExt = nameWithoutExtId ? stats.bySource?.[nameWithoutExtId] : undefined;
      if (
        fromNameWithoutExt &&
        (fromNameWithoutExt.success > 0 || fromNameWithoutExt.failure > 0)
      ) {
        return fromNameWithoutExt;
      }
    }
  }

  const fileSuccess = normalizeAuthFileUsageTotal(file.success);
  const fileFailure = normalizeAuthFileUsageTotal(file.failed);
  if (fileSuccess > 0 || fileFailure > 0) {
    return { success: fileSuccess, failure: fileFailure };
  }

  return defaultStats;
}

function normalizeApiKeyList(input: unknown): string[] {
  if (!Array.isArray(input)) return [];
  const seen = new Set<string>();
  const keys: string[] = [];

  input.forEach((item) => {
    const value = typeof item === 'string' ? item : item?.['api-key'] ?? item?.apiKey ?? '';
    const trimmed = String(value || '').trim();
    if (!trimmed || seen.has(trimmed)) return;
    seen.add(trimmed);
    keys.push(trimmed);
  });

  return keys;
}

export function AuthFilesPage() {
  const { t } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const resolvedTheme: ResolvedTheme = useThemeStore((state) => state.resolvedTheme);

  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState<'all' | string>('all');
  const [search, setSearch] = useState('');
  const [uploading, setUploading] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [deletingAll, setDeletingAll] = useState(false);
  const [deleting401, setDeleting401] = useState(false);
  const [statusUpdating, setStatusUpdating] = useState<Record<string, boolean>>({});
  const [cooldownResetting, setCooldownResetting] = useState<Record<string, boolean>>({});
  const [resettingAllCooldowns, setResettingAllCooldowns] = useState(false);
  const [quotaRefreshingAll, setQuotaRefreshingAll] = useState(false);
  const [quotaRefreshingSingle, setQuotaRefreshingSingle] = useState<Record<string, boolean>>({});
  const [apiKeyAuthMap, setApiKeyAuthMap] = useState<Record<string, string[]>>({});
  const [apiKeys, setApiKeys] = useState<string[]>([]);
  const [keyStats, setKeyStats] = useState<KeyStats>({ bySource: {}, byAuthIndex: {} });
  const [usageDetails, setUsageDetails] = useState<UsageDetail[]>([]);

  // 详情弹窗相关
  const [detailModalOpen, setDetailModalOpen] = useState(false);
  const [selectedFile, setSelectedFile] = useState<AuthFileItem | null>(null);

  // 模型列表弹窗相关
  const [modelsModalOpen, setModelsModalOpen] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsList, setModelsList] = useState<AuthFileModelItem[]>([]);
  const [modelsFileName, setModelsFileName] = useState('');
  const [modelsFileType, setModelsFileType] = useState('');
  const [modelsError, setModelsError] = useState<'unsupported' | null>(null);
  const modelsCacheRef = useRef<Map<string, AuthFileModelItem[]>>(new Map());

  // OAuth 排除模型相关
  const [excluded, setExcluded] = useState<Record<string, string[]>>({});
  const [excludedError, setExcludedError] = useState<'unsupported' | null>(null);
  const [excludedModalOpen, setExcludedModalOpen] = useState(false);
  const [excludedForm, setExcludedForm] = useState<ExcludedFormState>({
    provider: '',
    modelsText: '',
  });
  const [savingExcluded, setSavingExcluded] = useState(false);

  // OAuth 模型映射相关
  const [modelMappings, setModelMappings] = useState<Record<string, OAuthModelMappingEntry[]>>({});
  const [modelMappingsError, setModelMappingsError] = useState<'unsupported' | null>(null);
  const [mappingModalOpen, setMappingModalOpen] = useState(false);
  const [mappingForm, setMappingForm] = useState<ModelMappingsFormState>({
    provider: '',
    mappings: [buildEmptyMappingEntry()],
  });
  const [mappingModelsFileName, setMappingModelsFileName] = useState('');
  const [mappingModelsList, setMappingModelsList] = useState<AuthFileModelItem[]>([]);
  const [mappingModelsLoading, setMappingModelsLoading] = useState(false);
  const [mappingModelsError, setMappingModelsError] = useState<'unsupported' | null>(null);
  const [savingMappings, setSavingMappings] = useState(false);

  const [metadataEditor, setMetadataEditor] = useState<AuthFileMetadataEditorState | null>(null);
  const [uploadMenuOpen, setUploadMenuOpen] = useState(false);
  const [pasteModalOpen, setPasteModalOpen] = useState(false);
  const [pasteFileName, setPasteFileName] = useState('');
  const [pasteJsonText, setPasteJsonText] = useState('');
  const [pasteUploading, setPasteUploading] = useState(false);

  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const uploadMenuRef = useRef<HTMLDivElement | null>(null);
  const loadingKeyStatsRef = useRef(false);
  const excludedUnsupportedRef = useRef(false);
  const mappingsUnsupportedRef = useRef(false);
  const autoLoadedQuotaSignatureRef = useRef('');

  const normalizeProviderKey = (value: string) => value.trim().toLowerCase();

  const disableControls = connectionStatus !== 'connected';
  const { quota: antigravityQuota, loadQuota: loadAntigravityQuota } =
    useQuotaLoader(ANTIGRAVITY_CONFIG);
  const { quota: codexQuota, loadQuota: loadCodexQuota } = useQuotaLoader(CODEX_CONFIG);
  const { quota: geminiCliQuota, loadQuota: loadGeminiCliQuota } =
    useQuotaLoader(GEMINI_CLI_CONFIG);

  const setQuotaLoadingNoop = useCallback((_loading: boolean, _scope?: 'page' | 'all' | null) => {
    // Inline quota does not need extra section-level loading state.
  }, []);

  const loadInlineQuotas = useCallback(
    async (targets: AuthFileItem[]) => {
      if (targets.length === 0) return;
      await Promise.all([
        loadAntigravityQuota(
          targets.filter((file) => ANTIGRAVITY_CONFIG.filterFn(file)),
          'all',
          setQuotaLoadingNoop
        ),
        loadCodexQuota(
          targets.filter((file) => CODEX_CONFIG.filterFn(file)),
          'all',
          setQuotaLoadingNoop
        ),
        loadGeminiCliQuota(
          targets.filter((file) => GEMINI_CLI_CONFIG.filterFn(file)),
          'all',
          setQuotaLoadingNoop
        ),
      ]);
    },
    [loadAntigravityQuota, loadCodexQuota, loadGeminiCliQuota, setQuotaLoadingNoop]
  );

  const supportsInlineQuota = useCallback((item: AuthFileItem) => {
    if (isAntigravityFile(item) || isCodexFile(item)) return true;
    if (isGeminiCliFile(item) && !isRuntimeOnlyAuthFile(item)) return true;
    return false;
  }, []);

  useEffect(() => {
    if (connectionStatus !== 'connected') {
      autoLoadedQuotaSignatureRef.current = '';
      return;
    }
    const targets = files.filter((item) => supportsInlineQuota(item));
    if (targets.length === 0) return;
    const signature = targets
      .map((item) =>
        [
          item.name,
          item.type || '',
          item.auth_index ?? item.authIndex ?? '',
          item.modified ?? item.modtime ?? '',
        ].join(':')
      )
      .sort()
      .join('|');
    if (signature === autoLoadedQuotaSignatureRef.current) return;
    autoLoadedQuotaSignatureRef.current = signature;
    void loadInlineQuotas(targets);
  }, [connectionStatus, files, loadInlineQuotas, supportsInlineQuota]);

  useEffect(() => {
    if (!uploadMenuOpen) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (!uploadMenuRef.current) return;
      if (uploadMenuRef.current.contains(event.target as Node)) return;
      setUploadMenuOpen(false);
    };
    document.addEventListener('mousedown', handlePointerDown);
    return () => document.removeEventListener('mousedown', handlePointerDown);
  }, [uploadMenuOpen]);

  const modelSourceFileOptions = useMemo(() => {
    const normalizedProvider = normalizeProviderKey(mappingForm.provider);
    const matching: string[] = [];
    const others: string[] = [];
    const seen = new Set<string>();

    files.forEach((file) => {
      const isRuntimeOnly = isRuntimeOnlyAuthFile(file);
      const isAistudio = (file.type || '').toLowerCase() === 'aistudio';
      const canShowModels = !isRuntimeOnly || isAistudio;
      if (!canShowModels) return;

      const fileName = String(file.name || '').trim();
      if (!fileName) return;
      if (seen.has(fileName)) return;
      seen.add(fileName);

      if (!normalizedProvider) {
        matching.push(fileName);
        return;
      }

      const typeKey = normalizeProviderKey(String(file.type || ''));
      const providerKey = normalizeProviderKey(String(file.provider || ''));
      const isMatch = typeKey === normalizedProvider || providerKey === normalizedProvider;
      if (isMatch) {
        matching.push(fileName);
      } else {
        others.push(fileName);
      }
    });

    matching.sort((a, b) => a.localeCompare(b));
    others.sort((a, b) => a.localeCompare(b));
    return [...matching, ...others];
  }, [files, mappingForm.provider]);

  useEffect(() => {
    if (!mappingModalOpen) return;

    const fileName = mappingModelsFileName.trim();
    if (!fileName) {
      setMappingModelsList([]);
      setMappingModelsError(null);
      setMappingModelsLoading(false);
      return;
    }

    const cached = modelsCacheRef.current.get(fileName);
    if (cached) {
      setMappingModelsList(cached);
      setMappingModelsError(null);
    }

    let cancelled = false;
    setMappingModelsLoading(true);
    setMappingModelsError(null);

    authFilesApi
      .getModelsForAuthFile(fileName, undefined, true)
      .then((models) => {
        if (cancelled) return;
        modelsCacheRef.current.set(fileName, models);
        setMappingModelsList(models);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const errorMessage = err instanceof Error ? err.message : '';
        if (
          errorMessage.includes('404') ||
          errorMessage.includes('not found') ||
          errorMessage.includes('Not Found')
        ) {
          setMappingModelsList([]);
          setMappingModelsError('unsupported');
          return;
        }
        showNotification(`${t('notification.load_failed')}: ${errorMessage}`, 'error');
      })
      .finally(() => {
        if (cancelled) return;
        setMappingModelsLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [mappingModalOpen, mappingModelsFileName, showNotification, t]);

  // 格式化修改时间
  const formatCompactDateTime = (value: unknown): string => {
    const date = parseTimestampValue(value);
    if (!date) return '-';
    return date.toLocaleString(undefined, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  };

  const formatModified = (item: AuthFileItem): string => {
    const raw = item['modtime'] ?? item.modified;
    return formatCompactDateTime(raw);
  };

  const formatImportedAt = (item: AuthFileItem): string => {
    const raw = item['imported_at'] ?? item.importedAt ?? item['created_at'];
    return formatCompactDateTime(raw);
  };

  const getAuthFileDisplayName = (item: AuthFileItem): string => {
    return String(item['display_name'] ?? item.displayName ?? '').trim();
  };

  const getAuthFileTags = (item: AuthFileItem): string[] => {
    const raw = item.tags;
    if (!Array.isArray(raw)) return [];
    const seen = new Set<string>();
    return raw
      .map((tag) => String(tag ?? '').trim())
      .filter((tag) => {
        if (!tag) return false;
        const key = tag.toLowerCase();
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  };

  const normalizeAuthFileNameInput = (value: string): string => {
    const trimmed = value.trim();
    if (!trimmed) return '';
    return trimmed.toLowerCase().endsWith('.json') ? trimmed : `${trimmed}.json`;
  };

  const splitTagsText = (value: string): string[] => {
    const seen = new Set<string>();
    return value
      .split(/[\n,]+/)
      .map((tag) => tag.trim())
      .filter((tag) => {
        if (!tag) return false;
        const key = tag.toLowerCase();
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  };

  const resolveDisabledReason = (item: AuthFileItem): string | null => {
    if (!isEffectiveDisabled(item)) return null;
    const cooldownActive = isCooldownDisabled(item);
    const cooldownUntilRaw = item['cooldown_until'] ?? item.cooldownUntil;
    const cooldownUntil = parseTimestampValue(cooldownUntilRaw);
    const untilText = cooldownUntil ? cooldownUntil.toLocaleString() : null;
    const reasonRaw = String(item['disabled_reason'] ?? item.disabledReason ?? '').trim().toLowerCase();
    const msUntilRecover = cooldownUntil ? cooldownUntil.getTime() - Date.now() : null;
    const likelyWeeklyWindow = msUntilRecover !== null && msUntilRecover >= 24 * 60 * 60 * 1000;

    if (!cooldownActive || reasonRaw === 'manual' || item.disabled === true) {
      return t('auth_files.disabled_reason_manual');
    }
    if (reasonRaw === 'codex_5h_limit') {
      if (likelyWeeklyWindow) {
        return untilText
          ? t('auth_files.disabled_reason_cooldown_weekly_until', { time: untilText })
          : t('auth_files.disabled_reason_cooldown_weekly');
      }
      return untilText
        ? t('auth_files.disabled_reason_cooldown_5h_until', { time: untilText })
        : t('auth_files.disabled_reason_cooldown_5h');
    }
    if (reasonRaw === 'codex_weekly_limit') {
      return untilText
        ? t('auth_files.disabled_reason_cooldown_weekly_until', { time: untilText })
        : t('auth_files.disabled_reason_cooldown_weekly');
    }
    if (reasonRaw === 'codex_code_review_limit') {
      return untilText
        ? t('auth_files.disabled_reason_cooldown_code_review_until', { time: untilText })
        : t('auth_files.disabled_reason_cooldown_code_review');
    }
    return untilText
      ? t('auth_files.disabled_reason_cooldown_generic_until', { time: untilText })
      : t('auth_files.disabled_reason_cooldown_generic');
  };

  // 加载文件列表
  const loadFiles = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    if (!silent) {
      setLoading(true);
      setError('');
    }
    try {
      const data = await authFilesApi.list();
      setFiles(data?.files || []);
    } catch (err: unknown) {
      if (!silent) {
        const errorMessage = err instanceof Error ? err.message : t('notification.refresh_failed');
        setError(errorMessage);
      }
    } finally {
      if (!silent) {
        setLoading(false);
      }
    }
  }, [t]);

  // 加载 key 统计和 usage 明细（API 层已有60秒超时）
  const loadKeyStats = useCallback(async () => {
    // 防止重复请求
    if (loadingKeyStatsRef.current) return;
    loadingKeyStatsRef.current = true;
    try {
      const usageResponse = await usageApi.getUsage();
      const usageData = usageResponse?.usage ?? usageResponse;
      const stats = await usageApi.getKeyStats(usageData);
      setKeyStats(stats);
      // 收集 usage 明细用于状态栏
      const details = collectUsageDetails(usageData);
      setUsageDetails(details);
    } catch {
      // 静默失败
    } finally {
      loadingKeyStatsRef.current = false;
    }
  }, []);

  const loadApiKeyAuthMapping = useCallback(async () => {
    try {
      const [mapping, keys] = await Promise.all([apiKeysApi.getAuthMapping(), apiKeysApi.list()]);
      setApiKeyAuthMap(mapping || {});
      setApiKeys(normalizeApiKeyList(keys));
    } catch {
      setApiKeyAuthMap({});
      setApiKeys([]);
    }
  }, []);

  // 加载 OAuth 排除列表
  const loadExcluded = useCallback(async () => {
    try {
      const res = await authFilesApi.getOauthExcludedModels();
      excludedUnsupportedRef.current = false;
      setExcluded(res || {});
      setExcludedError(null);
    } catch (err: unknown) {
      const status =
        typeof err === 'object' && err !== null && 'status' in err
          ? (err as { status?: unknown }).status
          : undefined;

      if (status === 404) {
        setExcluded({});
        setExcludedError('unsupported');
        if (!excludedUnsupportedRef.current) {
          excludedUnsupportedRef.current = true;
          showNotification(t('oauth_excluded.upgrade_required'), 'warning');
        }
        return;
      }
      // 静默失败
    }
  }, [showNotification, t]);

  // 加载 OAuth 模型映射
  const loadModelMappings = useCallback(async () => {
    try {
      const res = await authFilesApi.getOauthModelMappings();
      mappingsUnsupportedRef.current = false;
      setModelMappings(res || {});
      setModelMappingsError(null);
    } catch (err: unknown) {
      const status =
        typeof err === 'object' && err !== null && 'status' in err
          ? (err as { status?: unknown }).status
          : undefined;

      if (status === 404) {
        setModelMappings({});
        setModelMappingsError('unsupported');
        if (!mappingsUnsupportedRef.current) {
          mappingsUnsupportedRef.current = true;
          showNotification(t('oauth_model_mappings.upgrade_required'), 'warning');
        }
        return;
      }
      // 静默失败
    }
  }, [showNotification, t]);

  const handleHeaderRefresh = useCallback(async () => {
    modelsCacheRef.current.clear();
    await Promise.all([
      loadFiles(),
      loadKeyStats(),
      loadExcluded(),
      loadModelMappings(),
      loadApiKeyAuthMapping(),
    ]);
  }, [loadFiles, loadKeyStats, loadExcluded, loadModelMappings, loadApiKeyAuthMapping]);

  useHeaderRefresh(handleHeaderRefresh);

  useEffect(() => {
    loadFiles();
    loadKeyStats();
    loadExcluded();
    loadModelMappings();
    loadApiKeyAuthMapping();
  }, [loadFiles, loadKeyStats, loadExcluded, loadModelMappings, loadApiKeyAuthMapping]);

  // Keep auth cooldown/disabled status in sync with runtime state.
  useInterval(() => {
    if (connectionStatus !== 'connected') return;
    void loadFiles({ silent: true });
  }, 15_000);

  // 定时刷新状态数据（每240秒）
  useInterval(loadKeyStats, 240_000);

  // 提取所有存在的类型
  const existingTypes = useMemo(() => {
    const types = new Set<string>(['all']);
    files.forEach((file) => {
      if (file.type) {
        types.add(file.type);
      }
    });
    return Array.from(types);
  }, [files]);

  const excludedProviderLookup = useMemo(() => {
    const lookup = new Map<string, string>();
    Object.keys(excluded).forEach((provider) => {
      const key = provider.trim().toLowerCase();
      if (key && !lookup.has(key)) {
        lookup.set(key, provider);
      }
    });
    return lookup;
  }, [excluded]);

  const mappingProviderLookup = useMemo(() => {
    const lookup = new Map<string, string>();
    Object.keys(modelMappings).forEach((provider) => {
      const key = provider.trim().toLowerCase();
      if (key && !lookup.has(key)) {
        lookup.set(key, provider);
      }
    });
    return lookup;
  }, [modelMappings]);

  const providerOptions = useMemo(() => {
    const extraProviders = new Set<string>();

    Object.keys(excluded).forEach((provider) => {
      extraProviders.add(provider);
    });
    Object.keys(modelMappings).forEach((provider) => {
      extraProviders.add(provider);
    });
    files.forEach((file) => {
      if (typeof file.type === 'string') {
        extraProviders.add(file.type);
      }
      if (typeof file.provider === 'string') {
        extraProviders.add(file.provider);
      }
    });

    const normalizedExtras = Array.from(extraProviders)
      .map((value) => value.trim())
      .filter((value) => value && !OAUTH_PROVIDER_EXCLUDES.has(value.toLowerCase()));

    const baseSet = new Set(OAUTH_PROVIDER_PRESETS.map((value) => value.toLowerCase()));
    const extraList = normalizedExtras
      .filter((value) => !baseSet.has(value.toLowerCase()))
      .sort((a, b) => a.localeCompare(b));

    return [...OAUTH_PROVIDER_PRESETS, ...extraList];
  }, [excluded, files, modelMappings]);

  // 过滤和搜索
  const filtered = useMemo(() => {
    return files.filter((item) => {
      const matchType = filter === 'all' || item.type === filter;
      const term = search.trim().toLowerCase();
      const displayName = String(item['display_name'] ?? item.displayName ?? '').toLowerCase();
      const tagText = Array.isArray(item.tags) ? item.tags.join(' ').toLowerCase() : '';
      const matchSearch =
        !term ||
        item.name.toLowerCase().includes(term) ||
        displayName.includes(term) ||
        tagText.includes(term) ||
        (item.type || '').toString().toLowerCase().includes(term) ||
        (item.provider || '').toString().toLowerCase().includes(term);
      return matchType && matchSearch;
    });
  }, [files, filter, search]);

  const authAssignments = useMemo(() => {
    return Object.values(apiKeyAuthMap || {})
      .filter((refs): refs is string[] => Array.isArray(refs) && refs.length > 0)
      .map((refs) => {
        return new Set(
          refs
            .map((ref) => String(ref ?? '').trim())
            .filter(Boolean)
        );
      })
      .filter((refs) => refs.size > 0);
  }, [apiKeyAuthMap]);

  const implicitAllApiKeyCount = useMemo(() => {
    if (apiKeys.length === 0) return 0;

    const restrictedKeys = new Set(
      Object.keys(apiKeyAuthMap || {})
        .map((key) => String(key ?? '').trim())
        .filter(Boolean)
    );

    let count = 0;
    apiKeys.forEach((key) => {
      if (!restrictedKeys.has(key)) count += 1;
    });
    return count;
  }, [apiKeyAuthMap, apiKeys]);

  const getAuthFileAssignmentCount = useCallback((item: AuthFileItem) => {
    if (authAssignments.length === 0 && implicitAllApiKeyCount === 0) return 0;

    const candidates = [
      String(item?.name ?? '').trim(),
      String(item.id ?? '').trim(),
      String(item.auth_index ?? '').trim(),
      String(item.authIndex ?? '').trim(),
    ].filter(Boolean);

    let matched = implicitAllApiKeyCount;
    if (candidates.length === 0) return matched;

    authAssignments.forEach((refs) => {
      if (candidates.some((candidate) => refs.has(candidate))) {
        matched += 1;
      }
    });

    return matched;
  }, [authAssignments, implicitAllApiKeyCount]);

  // 认证文件默认全量显示（不分页）
  const pageItems = filtered;
  const hasQuotaTargets = useMemo(
    () => files.some((item) => supportsInlineQuota(item)),
    [files, supportsInlineQuota]
  );
  const hasCooldownTargets = useMemo(
    () => files.some((item) => isCooldownDisabled(item)),
    [files]
  );
  const getQuotaStateForFile = useCallback((item: AuthFileItem): QuotaStatusState | undefined => {
    const key = String(item.name || '').trim();
    if (!key) return undefined;
    if (isAntigravityFile(item)) return antigravityQuota[key] as QuotaStatusState | undefined;
    if (isCodexFile(item)) return codexQuota[key] as QuotaStatusState | undefined;
    if (isGeminiCliFile(item) && !isRuntimeOnlyAuthFile(item)) {
      return geminiCliQuota[key] as QuotaStatusState | undefined;
    }
    return undefined;
  }, [antigravityQuota, codexQuota, geminiCliQuota]);

  const hasAvailableQuota = useCallback((item: AuthFileItem): boolean => {
    const key = String(item.name || '').trim();
    if (!key || !supportsInlineQuota(item)) return false;

    if (isAntigravityFile(item)) {
      const quota = antigravityQuota[key] as AntigravityQuotaState | undefined;
      if (!quota || quota.status !== 'success') return false;
      return (quota.groups ?? []).some((group) => hasPositiveNumber(group.remainingFraction));
    }

    if (isCodexFile(item)) {
      const quota = codexQuota[key] as CodexQuotaState | undefined;
      if (!quota || quota.status !== 'success') return false;
      return (quota.windows ?? []).some((window) => {
        const usedPercent = Number(window.usedPercent);
        if (!Number.isFinite(usedPercent)) return false;
        return usedPercent < CODEX_QUOTA_USED_PERCENT_EXHAUSTED_THRESHOLD;
      });
    }

    if (isGeminiCliFile(item) && !isRuntimeOnlyAuthFile(item)) {
      const quota = geminiCliQuota[key] as GeminiCliQuotaState | undefined;
      if (!quota || quota.status !== 'success') return false;
      return (quota.buckets ?? []).some((bucket) => {
        if (hasPositiveNumber(bucket.remainingAmount)) return true;
        return hasPositiveNumber(bucket.remainingFraction);
      });
    }

    return false;
  }, [antigravityQuota, codexQuota, geminiCliQuota, supportsInlineQuota]);

  const enabledWithQuotaCount = useMemo(() => {
    return files.reduce((count, file) => {
      if (isRuntimeOnlyAuthFile(file)) return count;
      if (isEffectiveDisabled(file)) return count;
      if (!hasAvailableQuota(file)) return count;
      return count + 1;
    }, 0);
  }, [files, hasAvailableQuota]);

  const invalidated401FileNames = useMemo(() => {
    const flagged = new Set<string>();
    files.forEach((file) => {
      const key = String(file.name || '').trim();
      if (!key) return;
      if (!supportsInlineQuota(file)) return;
      const quota = getQuotaStateForFile(file);
      if (!quota || quota.status !== 'error') return;
      if (isQuotaTokenInvalidated401(quota.errorStatus, quota.error)) {
        flagged.add(key);
      }
    });
    return flagged;
  }, [files, getQuotaStateForFile, supportsInlineQuota]);

  const hasInvalidated401Targets = invalidated401FileNames.size > 0;

  const handleRefreshAllQuotas = useCallback(async () => {
    const targets = files.filter((item) => supportsInlineQuota(item));
    if (targets.length === 0) return;
    setQuotaRefreshingAll(true);
    try {
      await loadInlineQuotas(targets);
      await loadFiles({ silent: true });
    } finally {
      setQuotaRefreshingAll(false);
    }
  }, [files, loadFiles, loadInlineQuotas, supportsInlineQuota]);

  const handleRefreshSingleQuota = useCallback(async (item: AuthFileItem) => {
    if (!supportsInlineQuota(item)) return;
    const key = String(item.name || '').trim();
    if (!key) return;
    setQuotaRefreshingSingle((prev) => ({ ...prev, [key]: true }));
    try {
      await loadInlineQuotas([item]);
      await loadFiles({ silent: true });
    } finally {
      setQuotaRefreshingSingle((prev) => {
        const next = { ...prev };
        delete next[key];
        return next;
      });
    }
  }, [loadFiles, loadInlineQuotas, supportsInlineQuota]);

  const openUploadPasteModal = () => {
    setUploadMenuOpen(false);
    setPasteFileName('');
    setPasteJsonText('');
    setPasteModalOpen(true);
  };

  const openUploadFilePicker = () => {
    setUploadMenuOpen(false);
    fileInputRef.current?.click();
  };

  const handleUploadClick = () => {
    setUploadMenuOpen((prev) => !prev);
  };

  // 处理文件上传（支持多选）
  const handleFileChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const fileList = event.target.files;
    if (!fileList || fileList.length === 0) return;

    setUploadMenuOpen(false);
    const filesToUpload = Array.from(fileList);
    const validFiles: File[] = [];
    const invalidFiles: string[] = [];
    const oversizedFiles: string[] = [];

    filesToUpload.forEach((file) => {
      if (!file.name.endsWith('.json')) {
        invalidFiles.push(file.name);
        return;
      }
      if (file.size > MAX_AUTH_FILE_SIZE) {
        oversizedFiles.push(file.name);
        return;
      }
      validFiles.push(file);
    });

    if (invalidFiles.length > 0) {
      showNotification(t('auth_files.upload_error_json'), 'error');
    }
    if (oversizedFiles.length > 0) {
      showNotification(
        t('auth_files.upload_error_size', { maxSize: formatFileSize(MAX_AUTH_FILE_SIZE) }),
        'error'
      );
    }

    if (validFiles.length === 0) {
      event.target.value = '';
      return;
    }

    setUploading(true);
    let successCount = 0;
    const failed: { name: string; message: string }[] = [];

    for (const file of validFiles) {
      try {
        await authFilesApi.upload(file);
        successCount++;
      } catch (err: unknown) {
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        failed.push({ name: file.name, message: errorMessage });
      }
    }

    if (successCount > 0) {
      const suffix = validFiles.length > 1 ? ` (${successCount}/${validFiles.length})` : '';
      showNotification(
        `${t('auth_files.upload_success')}${suffix}`,
        failed.length ? 'warning' : 'success'
      );
      await loadFiles();
      await loadKeyStats();
      await loadApiKeyAuthMapping();
    }

    if (failed.length > 0) {
      const details = failed.map((item) => `${item.name}: ${item.message}`).join('; ');
      showNotification(`${t('notification.upload_failed')}: ${details}`, 'error');
    }

    setUploading(false);
    event.target.value = '';
  };

  const handlePasteUpload = async () => {
    const name = normalizeAuthFileNameInput(pasteFileName);
    const text = pasteJsonText.trim();
    if (!name) {
      showNotification(t('auth_files.upload_paste_name_required'), 'error');
      return;
    }
    if (!text) {
      showNotification(t('auth_files.upload_paste_body_required'), 'error');
      return;
    }
    if (new Blob([text]).size > MAX_AUTH_FILE_SIZE) {
      showNotification(
        t('auth_files.upload_error_size', { maxSize: formatFileSize(MAX_AUTH_FILE_SIZE) }),
        'error'
      );
      return;
    }
    try {
      JSON.parse(text);
    } catch {
      showNotification(t('auth_files.upload_paste_invalid_json'), 'error');
      return;
    }

    setPasteUploading(true);
    try {
      await authFilesApi.uploadText(name, text);
      showNotification(t('auth_files.upload_success'), 'success');
      setPasteModalOpen(false);
      setPasteFileName('');
      setPasteJsonText('');
      await loadFiles();
      await loadKeyStats();
      await loadApiKeyAuthMapping();
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Unknown error';
      showNotification(`${t('notification.upload_failed')}: ${errorMessage}`, 'error');
    } finally {
      setPasteUploading(false);
    }
  };

  const openMetadataEditor = async (item: AuthFileItem) => {
    const name = String(item.name || '').trim();
    if (!name) return;
    setMetadataEditor({
      originalName: name,
      fileName: name,
      displayName: getAuthFileDisplayName(item),
      tagsText: getAuthFileTags(item).join(', '),
      loadingFile: true,
      fileError: null,
      originalJsonText: '',
      jsonText: '',
      json: null,
      saving: false,
    });

    try {
      const rawText = await authFilesApi.downloadText(name);
      const trimmed = rawText.trim();
      let parsed: unknown;
      try {
        parsed = JSON.parse(trimmed) as unknown;
      } catch {
        setMetadataEditor((prev) => {
          if (!prev || prev.originalName !== name) return prev;
          return {
            ...prev,
            loadingFile: false,
            fileError: t('auth_files.prefix_proxy_invalid_json'),
            originalJsonText: trimmed,
            jsonText: trimmed,
            json: null,
          };
        });
        return;
      }

      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        setMetadataEditor((prev) => {
          if (!prev || prev.originalName !== name) return prev;
          return {
            ...prev,
            loadingFile: false,
            fileError: t('auth_files.prefix_proxy_invalid_json'),
            originalJsonText: trimmed,
            jsonText: trimmed,
            json: null,
          };
        });
        return;
      }

      const json = parsed as Record<string, unknown>;
      const formatted = JSON.stringify(json, null, 2);
      setMetadataEditor((prev) => {
        if (!prev || prev.originalName !== name) return prev;
        return {
          ...prev,
          loadingFile: false,
          fileError: null,
          originalJsonText: formatted,
          jsonText: formatted,
          json,
        };
      });
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : t('notification.download_failed');
      setMetadataEditor((prev) => {
        if (!prev || prev.originalName !== name) return prev;
        return {
          ...prev,
          loadingFile: false,
          fileError: errorMessage,
          originalJsonText: '',
          jsonText: '',
          json: null,
        };
      });
      showNotification(`${t('notification.download_failed')}: ${errorMessage}`, 'error');
    }
  };

  const handleMetadataJsonChange = (value: string) => {
    setMetadataEditor((prev) => {
      if (!prev) return prev;
      try {
        const parsed = JSON.parse(value) as unknown;
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          return {
            ...prev,
            jsonText: value,
            json: null,
            fileError: t('auth_files.prefix_proxy_invalid_json'),
          };
        }
        return {
          ...prev,
          jsonText: value,
          json: parsed as Record<string, unknown>,
          fileError: null,
        };
      } catch {
        return {
          ...prev,
          jsonText: value,
          json: null,
          fileError: t('auth_files.prefix_proxy_invalid_json'),
        };
      }
    });
  };

  const handleMetadataSave = async () => {
    if (!metadataEditor) return;
    const originalName = metadataEditor.originalName.trim();
    const nextName = normalizeAuthFileNameInput(metadataEditor.fileName);
    const displayName = metadataEditor.displayName.trim();
    const tags = splitTagsText(metadataEditor.tagsText);
    const sourceDirty = metadataEditor.jsonText !== metadataEditor.originalJsonText;

    if (!originalName || !nextName) {
      showNotification(t('auth_files.metadata_name_required'), 'error');
      return;
    }
    if (metadataEditor.loadingFile) {
      showNotification(t('auth_files.prefix_proxy_loading'), 'warning');
      return;
    }
    if (sourceDirty) {
      try {
        const parsed = JSON.parse(metadataEditor.jsonText) as unknown;
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          showNotification(t('auth_files.prefix_proxy_invalid_json'), 'error');
          return;
        }
      } catch {
        showNotification(t('auth_files.prefix_proxy_invalid_json'), 'error');
        return;
      }
      const fileSize = new Blob([metadataEditor.jsonText]).size;
      if (fileSize > MAX_AUTH_FILE_SIZE) {
        showNotification(
          t('auth_files.upload_error_size', { maxSize: formatFileSize(MAX_AUTH_FILE_SIZE) }),
          'error'
        );
        return;
      }
    }

    setMetadataEditor((prev) => (prev ? { ...prev, saving: true } : prev));
    try {
      if (sourceDirty) {
        const file = new File([metadataEditor.jsonText], originalName, { type: 'application/json' });
        await authFilesApi.upload(file);
      }
      if (nextName !== originalName) {
        await authFilesApi.rename(originalName, nextName);
      }
      await authFilesApi.updateMetadata(nextName, {
        display_name: displayName,
        tags,
      });
      showNotification(t('auth_files.metadata_save_success'), 'success');
      modelsCacheRef.current.delete(originalName);
      if (nextName !== originalName) {
        modelsCacheRef.current.delete(nextName);
      }
      autoLoadedQuotaSignatureRef.current = '';
      setMetadataEditor(null);
      await loadFiles();
      await loadKeyStats();
      await loadApiKeyAuthMapping();
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Unknown error';
      showNotification(`${t('notification.save_failed')}: ${errorMessage}`, 'error');
    } finally {
      setMetadataEditor((prev) => (prev ? { ...prev, saving: false } : prev));
    }
  };

  // 删除单个文件
  const handleDelete = async (name: string) => {
    showConfirmation({
      title: t('auth_files.delete_title', { defaultValue: 'Delete File' }),
      message: `${t('auth_files.delete_confirm')} "${name}" ?`,
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        setDeleting(name);
        try {
          await authFilesApi.deleteFile(name);
          showNotification(t('auth_files.delete_success'), 'success');
          setFiles((prev) => prev.filter((item) => item.name !== name));
        } catch (err: unknown) {
          const errorMessage = err instanceof Error ? err.message : '';
          showNotification(`${t('notification.delete_failed')}: ${errorMessage}`, 'error');
        } finally {
          setDeleting(null);
        }
      },
    });
  };

  // 删除全部（根据筛选类型）
  const handleDeleteAll = async () => {
    const isFiltered = filter !== 'all';
    const typeLabel = isFiltered ? getTypeLabel(filter) : t('auth_files.filter_all');
    const confirmMessage = isFiltered
      ? t('auth_files.delete_filtered_confirm', { type: typeLabel })
      : t('auth_files.delete_all_confirm');

    showConfirmation({
      title: t('auth_files.delete_all_title', { defaultValue: 'Delete All Files' }),
      message: confirmMessage,
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        setDeletingAll(true);
        try {
          if (!isFiltered) {
            // 删除全部
            await authFilesApi.deleteAll();
            showNotification(t('auth_files.delete_all_success'), 'success');
            setFiles((prev) => prev.filter((file) => isRuntimeOnlyAuthFile(file)));
          } else {
            // 删除筛选类型的文件
            const filesToDelete = files.filter((f) => f.type === filter && !isRuntimeOnlyAuthFile(f));

            if (filesToDelete.length === 0) {
              showNotification(t('auth_files.delete_filtered_none', { type: typeLabel }), 'info');
              setDeletingAll(false);
              return;
            }

            let success = 0;
            let failed = 0;
            const deletedNames: string[] = [];

            for (const file of filesToDelete) {
              try {
                await authFilesApi.deleteFile(file.name);
                success++;
                deletedNames.push(file.name);
              } catch {
                failed++;
              }
            }

            setFiles((prev) => prev.filter((f) => !deletedNames.includes(f.name)));

            if (failed === 0) {
              showNotification(
                t('auth_files.delete_filtered_success', { count: success, type: typeLabel }),
                'success'
              );
            } else {
              showNotification(
                t('auth_files.delete_filtered_partial', { success, failed, type: typeLabel }),
                'warning'
              );
            }
            setFilter('all');
          }
        } catch (err: unknown) {
          const errorMessage = err instanceof Error ? err.message : '';
          showNotification(`${t('notification.delete_failed')}: ${errorMessage}`, 'error');
        } finally {
          setDeletingAll(false);
        }
      },
    });
  };

  // 下载文件
  const handleDownload = async (name: string) => {
    try {
      const response = await apiClient.getRaw(
        `/auth-files/download?name=${encodeURIComponent(name)}`,
        {
          responseType: 'blob',
        }
      );
      const blob = new Blob([response.data]);
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      a.click();
      window.URL.revokeObjectURL(url);
      showNotification(t('auth_files.download_success'), 'success');
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.download_failed')}: ${errorMessage}`, 'error');
    }
  };

  const handleStatusToggle = async (item: AuthFileItem, enabled: boolean) => {
    if (enabled && isCooldownDisabled(item) && item.disabled !== true) {
      await handleResetCooldown(item);
      return;
    }

    const name = item.name;
    const nextDisabled = !enabled;
    const previousDisabled = item.disabled === true;

    setStatusUpdating((prev) => ({ ...prev, [name]: true }));
    // Optimistic update for snappy UI.
    setFiles((prev) => prev.map((f) => (f.name === name ? { ...f, disabled: nextDisabled } : f)));

    try {
      const res = await authFilesApi.setStatus(name, nextDisabled);
      setFiles((prev) => prev.map((f) => (f.name === name ? { ...f, disabled: res.disabled } : f)));
      showNotification(
        enabled
          ? t('auth_files.status_enabled_success', { name })
          : t('auth_files.status_disabled_success', { name }),
        'success'
      );
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      setFiles((prev) =>
        prev.map((f) => (f.name === name ? { ...f, disabled: previousDisabled } : f))
      );
      showNotification(`${t('notification.update_failed')}: ${errorMessage}`, 'error');
    } finally {
      setStatusUpdating((prev) => {
        if (!prev[name]) return prev;
        const next = { ...prev };
        delete next[name];
        return next;
      });
    }
  };

  const handleResetCooldown = async (item: AuthFileItem) => {
    const name = String(item.name ?? '').trim();
    if (!name) return;

    setCooldownResetting((prev) => ({ ...prev, [name]: true }));
    try {
      const res = await authFilesApi.resetCooldown(name, true);
      await loadFiles({ silent: true });
      showNotification(
        t('auth_files.reset_cooldown_success', {
          name,
          count: Number(res?.reset ?? 0),
        }),
        'success'
      );
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.update_failed')}: ${errorMessage}`, 'error');
    } finally {
      setCooldownResetting((prev) => {
        if (!prev[name]) return prev;
        const next = { ...prev };
        delete next[name];
        return next;
      });
    }
  };

  const handleResetAllCooldowns = async () => {
    setResettingAllCooldowns(true);
    try {
      const res = await authFilesApi.resetAllCooldowns(true);
      await loadFiles({ silent: true });
      showNotification(
        t('auth_files.reset_all_cooldowns_success', {
          count: Number(res?.reset ?? 0),
        }),
        'success'
      );
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.update_failed')}: ${errorMessage}`, 'error');
    } finally {
      setResettingAllCooldowns(false);
    }
  };

  // 显示详情弹窗
  const showDetails = (file: AuthFileItem) => {
    setSelectedFile(file);
    setDetailModalOpen(true);
  };

  // 显示模型列表
  const showModels = async (item: AuthFileItem) => {
    const authID = String(item.id ?? '').trim() || undefined;
    setModelsFileName(item.name);
    setModelsFileType(item.type || '');
    setModelsError(null);
    setModelsModalOpen(true);

    const cached = modelsCacheRef.current.get(item.name);
    if (cached) {
      setModelsList(cached);
    } else {
      setModelsList([]);
    }

    setModelsLoading(true);
    try {
      const models = await authFilesApi.getModelsForAuthFile(item.name, authID, true);
      modelsCacheRef.current.set(item.name, models);
      setModelsList(models);
    } catch (err) {
      // 检测是否是 API 不支持的错误 (404 或特定错误消息)
      const errorMessage = err instanceof Error ? err.message : '';
      if (
        errorMessage.includes('404') ||
        errorMessage.includes('not found') ||
        errorMessage.includes('Not Found')
      ) {
        setModelsError('unsupported');
      } else {
        showNotification(`${t('notification.load_failed')}: ${errorMessage}`, 'error');
      }
    } finally {
      setModelsLoading(false);
    }
  };

  // 检查模型是否被 OAuth 排除
  const isModelExcluded = (modelId: string, providerType: string): boolean => {
    const providerKey = normalizeProviderKey(providerType);
    const excludedModels = excluded[providerKey] || excluded[providerType] || [];
    return excludedModels.some((pattern) => {
      if (pattern.includes('*')) {
        // 支持通配符匹配
        const regex = new RegExp('^' + pattern.replace(/\*/g, '.*') + '$', 'i');
        return regex.test(modelId);
      }
      return pattern.toLowerCase() === modelId.toLowerCase();
    });
  };

  // 获取类型标签显示文本
  const getTypeLabel = (type: string): string => {
    const key = `auth_files.filter_${type}`;
    const translated = t(key);
    if (translated !== key) return translated;
    if (type.toLowerCase() === 'iflow') return 'iFlow';
    return type.charAt(0).toUpperCase() + type.slice(1);
  };

  // 获取类型颜色
  const getTypeColor = (type: string): ThemeColors => {
    const set = TYPE_COLORS[type] || TYPE_COLORS.unknown;
    return resolvedTheme === 'dark' && set.dark ? set.dark : set.light;
  };

  // OAuth 排除相关方法
  const openExcludedModal = (provider?: string) => {
    const normalizedProvider = normalizeProviderKey(provider || '');
    const fallbackProvider =
      normalizedProvider || (filter !== 'all' ? normalizeProviderKey(String(filter)) : '');
    const lookupKey = fallbackProvider ? excludedProviderLookup.get(fallbackProvider) : undefined;
    const models = lookupKey ? excluded[lookupKey] : [];
    setExcludedForm({
      provider: lookupKey || fallbackProvider,
      modelsText: Array.isArray(models) ? models.join('\n') : '',
    });
    setExcludedModalOpen(true);
  };

  const saveExcludedModels = async () => {
    const provider = normalizeProviderKey(excludedForm.provider);
    if (!provider) {
      showNotification(t('oauth_excluded.provider_required'), 'error');
      return;
    }
    const models = excludedForm.modelsText
      .split(/[\n,]+/)
      .map((item) => item.trim())
      .filter(Boolean);
    setSavingExcluded(true);
    try {
      if (models.length) {
        await authFilesApi.saveOauthExcludedModels(provider, models);
      } else {
        await authFilesApi.deleteOauthExcludedEntry(provider);
      }
      await loadExcluded();
      showNotification(t('oauth_excluded.save_success'), 'success');
      setExcludedModalOpen(false);
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('oauth_excluded.save_failed')}: ${errorMessage}`, 'error');
    } finally {
      setSavingExcluded(false);
    }
  };

  const deleteExcluded = async (provider: string) => {
    const providerLabel = provider.trim() || provider;
    showConfirmation({
      title: t('oauth_excluded.delete_title', { defaultValue: 'Delete Exclusion' }),
      message: t('oauth_excluded.delete_confirm', { provider: providerLabel }),
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        const providerKey = normalizeProviderKey(provider);
        if (!providerKey) {
          showNotification(t('oauth_excluded.provider_required'), 'error');
          return;
        }
        try {
          await authFilesApi.deleteOauthExcludedEntry(providerKey);
          await loadExcluded();
          showNotification(t('oauth_excluded.delete_success'), 'success');
        } catch (err: unknown) {
          try {
            const current = await authFilesApi.getOauthExcludedModels();
            const next: Record<string, string[]> = {};
            Object.entries(current).forEach(([key, models]) => {
              if (normalizeProviderKey(key) === providerKey) return;
              next[key] = models;
            });
            await authFilesApi.replaceOauthExcludedModels(next);
            await loadExcluded();
            showNotification(t('oauth_excluded.delete_success'), 'success');
          } catch (fallbackErr: unknown) {
            const errorMessage =
              fallbackErr instanceof Error
                ? fallbackErr.message
                : err instanceof Error
                  ? err.message
                  : '';
            showNotification(`${t('oauth_excluded.delete_failed')}: ${errorMessage}`, 'error');
          }
        }
      },
    });
  };

  // OAuth 模型映射相关方法
  const normalizeMappingEntries = (
    entries?: OAuthModelMappingEntry[]
  ): OAuthModelMappingFormEntry[] => {
    if (!Array.isArray(entries) || entries.length === 0) {
      return [buildEmptyMappingEntry()];
    }
    return entries.map((entry) => ({
      id: generateId(),
      name: entry.name ?? '',
      alias: entry.alias ?? '',
      fork: Boolean(entry.fork),
    }));
  };

  const openMappingsModal = (provider?: string) => {
    const normalizedProvider = (provider || '').trim();
    const fallbackProvider = normalizedProvider || (filter !== 'all' ? String(filter) : '');
    const lookupKey = fallbackProvider
      ? mappingProviderLookup.get(fallbackProvider.toLowerCase())
      : undefined;
    const mappings = lookupKey ? modelMappings[lookupKey] : [];
    const providerValue = lookupKey || fallbackProvider;

    const normalizedProviderKey = normalizeProviderKey(providerValue);
    const defaultModelsFileName = files
      .filter((file) => {
        const isRuntimeOnly = isRuntimeOnlyAuthFile(file);
        const isAistudio = (file.type || '').toLowerCase() === 'aistudio';
        const canShowModels = !isRuntimeOnly || isAistudio;
        if (!canShowModels) return false;
        if (!normalizedProviderKey) return false;
        const typeKey = normalizeProviderKey(String(file.type || ''));
        const providerKey = normalizeProviderKey(String(file.provider || ''));
        return typeKey === normalizedProviderKey || providerKey === normalizedProviderKey;
      })
      .map((file) => file.name)
      .sort((a, b) => a.localeCompare(b))[0];

    setMappingForm({
      provider: providerValue,
      mappings: normalizeMappingEntries(mappings),
    });
    setMappingModelsFileName(defaultModelsFileName || '');
    setMappingModelsList([]);
    setMappingModelsError(null);
    setMappingModalOpen(true);
  };

  const updateMappingEntry = (
    index: number,
    field: keyof OAuthModelMappingEntry,
    value: string | boolean
  ) => {
    setMappingForm((prev) => ({
      ...prev,
      mappings: prev.mappings.map((entry, idx) =>
        idx === index ? { ...entry, [field]: value } : entry
      ),
    }));
  };

  const addMappingEntry = () => {
    setMappingForm((prev) => ({
      ...prev,
      mappings: [...prev.mappings, buildEmptyMappingEntry()],
    }));
  };

  const removeMappingEntry = (index: number) => {
    setMappingForm((prev) => {
      const next = prev.mappings.filter((_, idx) => idx !== index);
      return {
        ...prev,
        mappings: next.length ? next : [buildEmptyMappingEntry()],
      };
    });
  };

  const saveModelMappings = async () => {
    const provider = mappingForm.provider.trim();
    if (!provider) {
      showNotification(t('oauth_model_mappings.provider_required'), 'error');
      return;
    }

    const seen = new Set<string>();
    const mappings = mappingForm.mappings
      .map((entry) => {
        const name = String(entry.name ?? '').trim();
        const alias = String(entry.alias ?? '').trim();
        if (!name || !alias) return null;
        const key = `${name.toLowerCase()}::${alias.toLowerCase()}::${entry.fork ? '1' : '0'}`;
        if (seen.has(key)) return null;
        seen.add(key);
        return entry.fork ? { name, alias, fork: true } : { name, alias };
      })
      .filter(Boolean) as OAuthModelMappingEntry[];

    setSavingMappings(true);
    try {
      if (mappings.length) {
        await authFilesApi.saveOauthModelMappings(provider, mappings);
      } else {
        await authFilesApi.deleteOauthModelMappings(provider);
      }
      await loadModelMappings();
      showNotification(t('oauth_model_mappings.save_success'), 'success');
      setMappingModalOpen(false);
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('oauth_model_mappings.save_failed')}: ${errorMessage}`, 'error');
    } finally {
      setSavingMappings(false);
    }
  };

  const deleteModelMappings = async (provider: string) => {
    showConfirmation({
      title: t('oauth_model_mappings.delete_title', { defaultValue: 'Delete Mappings' }),
      message: t('oauth_model_mappings.delete_confirm', { provider }),
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        try {
          await authFilesApi.deleteOauthModelMappings(provider);
          await loadModelMappings();
          showNotification(t('oauth_model_mappings.delete_success'), 'success');
        } catch (err: unknown) {
          const errorMessage = err instanceof Error ? err.message : '';
          showNotification(`${t('oauth_model_mappings.delete_failed')}: ${errorMessage}`, 'error');
        }
      },
    });
  };

  // 渲染标签筛选器
  const renderFilterTags = () => (
    <div className={styles.filterTags}>
      {existingTypes.map((type) => {
        const isActive = filter === type;
        const color =
          type === 'all'
            ? { bg: 'var(--bg-tertiary)', text: 'var(--text-primary)' }
            : getTypeColor(type);
        const activeTextColor = resolvedTheme === 'dark' ? '#111827' : '#fff';
        return (
          <button
            key={type}
            className={`${styles.filterTag} ${isActive ? styles.filterTagActive : ''}`}
            style={{
              backgroundColor: isActive ? color.text : color.bg,
              color: isActive ? activeTextColor : color.text,
              borderColor: color.text,
            }}
            onClick={() => {
              setFilter(type);
            }}
          >
            {getTypeLabel(type)}
          </button>
        );
      })}
    </div>
  );

  // 预计算所有认证文件的状态栏数据（避免每次渲染重复计算）
  const statusBarCache = useMemo(() => {
    const cache = new Map<string, ReturnType<typeof calculateStatusBarData>>();

    files.forEach((file) => {
      const rawAuthIndex = file['auth_index'] ?? file.authIndex;
      const authIndexKey = normalizeAuthIndexValue(rawAuthIndex);

      if (authIndexKey) {
        // 过滤出属于该认证文件的 usage 明细
        const filteredDetails = usageDetails.filter((detail) => {
          const detailAuthIndex = normalizeAuthIndexValue(detail.auth_index);
          return detailAuthIndex !== null && detailAuthIndex === authIndexKey;
        });
        const recentStatus = statusBarDataFromAuthFileRecentRequests(
          file.recent_requests ?? file.recentRequests
        );
        cache.set(
          authIndexKey,
          filteredDetails.length > 0
            ? calculateStatusBarData(filteredDetails)
            : recentStatus || calculateStatusBarData([])
        );
      }
    });

    return cache;
  }, [usageDetails, files]);

  // 渲染状态监测栏
  const renderStatusBar = (item: AuthFileItem) => {
    // 认证文件使用 authIndex 来匹配 usage 数据
    const rawAuthIndex = item['auth_index'] ?? item.authIndex;
    const authIndexKey = normalizeAuthIndexValue(rawAuthIndex);

    const statusData =
      (authIndexKey && statusBarCache.get(authIndexKey)) || calculateStatusBarData([]);
    const hasData = statusData.totalSuccess + statusData.totalFailure > 0;
    const rateClass = !hasData
      ? ''
      : statusData.successRate >= 90
        ? styles.statusRateHigh
        : statusData.successRate >= 50
          ? styles.statusRateMedium
          : styles.statusRateLow;

    return (
      <div className={styles.statusBar}>
        <div className={styles.statusBlocks}>
          {statusData.blocks.map((state, idx) => {
            const blockClass =
              state === 'success'
                ? styles.statusBlockSuccess
                : state === 'failure'
                  ? styles.statusBlockFailure
                  : state === 'mixed'
                    ? styles.statusBlockMixed
                    : styles.statusBlockIdle;
            return <div key={idx} className={`${styles.statusBlock} ${blockClass}`} />;
          })}
        </div>
        <span className={`${styles.statusRate} ${rateClass}`}>
          {hasData ? `${statusData.successRate.toFixed(1)}%` : '--'}
        </span>
      </div>
    );
  };

  const handleDeleteInvalidated401 = useCallback(() => {
    const targets = files.filter((file) => {
      const name = String(file.name || '').trim();
      return name && !isRuntimeOnlyAuthFile(file) && invalidated401FileNames.has(name);
    });

    if (targets.length === 0) {
      showNotification(t('auth_files.delete_401_none'), 'info');
      return;
    }

    showConfirmation({
      title: t('auth_files.delete_401_title', { defaultValue: 'Delete Invalidated Files' }),
      message: t('auth_files.delete_401_confirm', { count: targets.length }),
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        setDeleting401(true);
        try {
          let success = 0;
          let failed = 0;
          const deletedNames: string[] = [];

          for (const file of targets) {
            try {
              await authFilesApi.deleteFile(file.name);
              success += 1;
              deletedNames.push(file.name);
            } catch {
              failed += 1;
            }
          }

          setFiles((prev) => prev.filter((file) => !deletedNames.includes(file.name)));

          if (failed === 0) {
            showNotification(t('auth_files.delete_401_success', { count: success }), 'success');
          } else {
            showNotification(
              t('auth_files.delete_401_partial', { success, failed }),
              success > 0 ? 'warning' : 'error'
            );
          }
        } catch (err: unknown) {
          const errorMessage = err instanceof Error ? err.message : '';
          showNotification(`${t('notification.delete_failed')}: ${errorMessage}`, 'error');
        } finally {
          setDeleting401(false);
        }
      },
    });
  }, [files, invalidated401FileNames, showConfirmation, showNotification, t]);

  const renderQuotaRing = (
    key: string,
    label: string,
    percent: number | null,
    title: string
  ) => {
    const radius = 15;
    const circumference = 2 * Math.PI * radius;
    const normalized = percent === null ? null : Math.max(0, Math.min(100, percent));
    const dashOffset =
      normalized === null ? circumference : circumference * (1 - normalized / 100);
    const ringClass =
      normalized === null
        ? styles.quotaRingUnknown
        : normalized >= 80
          ? styles.quotaRingHigh
          : normalized >= 50
            ? styles.quotaRingMedium
            : styles.quotaRingLow;
    const percentText = normalized === null ? '--' : `${Math.round(normalized)}%`;

    return (
      <div key={key} className={styles.quotaRingItem} title={title}>
        <svg className={styles.quotaRingSvg} viewBox="0 0 40 40" aria-hidden="true">
          <circle className={styles.quotaRingTrack} cx="20" cy="20" r={radius} />
          <circle
            className={`${styles.quotaRingValue} ${ringClass}`}
            cx="20"
            cy="20"
            r={radius}
            strokeDasharray={circumference}
            strokeDashoffset={dashOffset}
          />
        </svg>
        <span className={styles.quotaRingPercent}>{percentText}</span>
        <span className={styles.quotaRingLabel}>{label}</span>
      </div>
    );
  };

  const renderCompactQuota = (item: AuthFileItem) => {
    if (!supportsInlineQuota(item)) {
      return <span className={styles.tableMuted}>-</span>;
    }

    const quota = getQuotaStateForFile(item);
    const status = quota?.status ?? 'idle';
    if (status === 'loading') {
      return (
        <div className={styles.quotaCompactMessage}>
          <LoadingSpinner size={14} />
          <span>{t('common.loading')}</span>
        </div>
      );
    }
    if (status === 'error') {
      return (
        <span className={styles.quotaCompactError} title={quota?.error || t('common.unknown_error')}>
          ERR
        </span>
      );
    }
    if (!quota || status === 'idle') {
      if (isCodexFile(item)) {
        return (
          <div className={styles.quotaRingGroup}>
            {renderQuotaRing(
              'primary',
              '5h',
              null,
              `${t('codex_quota.primary_window')} · ${t('common.loading')}`
            )}
            {renderQuotaRing(
              'secondary',
              'Week',
              null,
              `${t('codex_quota.secondary_window')} · ${t('common.loading')}`
            )}
          </div>
        );
      }
      return <span className={styles.tableMuted}>-</span>;
    }

    if (isCodexFile(item)) {
      const codex = quota as CodexQuotaState;
      const windows = codex.windows ?? [];
      const primary = windows.find((window) => window.id === 'primary') ?? windows[0];
      const secondary = windows.find((window) => window.id === 'secondary') ?? windows[1];
      const toRemaining = (usedPercent: number | null | undefined) => {
        if (usedPercent === null || usedPercent === undefined) return null;
        const used = Number(usedPercent);
        if (!Number.isFinite(used)) return null;
        return Math.max(0, Math.min(100, 100 - used));
      };

      return (
        <div className={styles.quotaRingGroup}>
          {renderQuotaRing(
            'primary',
            '5h',
            toRemaining(primary?.usedPercent),
            `${primary?.label || t('codex_quota.primary_window')} · ${primary?.resetLabel || '-'}`
          )}
          {renderQuotaRing(
            'secondary',
            'Week',
            toRemaining(secondary?.usedPercent),
            `${secondary?.label || t('codex_quota.secondary_window')} · ${secondary?.resetLabel || '-'}`
          )}
        </div>
      );
    }

    if (isAntigravityFile(item)) {
      const antigravity = quota as AntigravityQuotaState;
      const group = antigravity.groups?.[0];
      const percent =
        group?.remainingFraction === null || group?.remainingFraction === undefined
          ? null
          : Math.round(Math.max(0, Math.min(1, group.remainingFraction)) * 100);
      return (
        <div className={styles.quotaRingGroup}>
          {renderQuotaRing('antigravity', 'Quota', percent, group?.label || 'Quota')}
        </div>
      );
    }

    if (isGeminiCliFile(item) && !isRuntimeOnlyAuthFile(item)) {
      const gemini = quota as GeminiCliQuotaState;
      const bucket = gemini.buckets?.[0];
      const percent =
        bucket?.remainingFraction === null || bucket?.remainingFraction === undefined
          ? null
          : Math.round(Math.max(0, Math.min(1, bucket.remainingFraction)) * 100);
      return (
        <div className={styles.quotaRingGroup}>
          {renderQuotaRing('gemini-cli', 'Quota', percent, bucket?.label || 'Quota')}
        </div>
      );
    }

    return <span className={styles.tableMuted}>-</span>;
  };

  const renderFileRow = (item: AuthFileItem) => {
    const fileStats = resolveAuthFileStats(item, keyStats);
    const isRuntimeOnly = isRuntimeOnlyAuthFile(item);
    const effectiveDisabled = isEffectiveDisabled(item);
    const cooldownActive = isCooldownDisabled(item);
    const disabledReason = resolveDisabledReason(item);
    const isInvalidated401 = invalidated401FileNames.has(String(item.name || '').trim());
    const isAistudio = (item.type || '').toLowerCase() === 'aistudio';
    const showModelsButton = !isRuntimeOnly || isAistudio;
    const typeColor = getTypeColor(item.type || 'unknown');
    const showQuotaRefreshButton = supportsInlineQuota(item);
    const refreshingSingleQuota = quotaRefreshingSingle[item.name] === true;
    const assignedApiKeyCount = getAuthFileAssignmentCount(item);
    const displayName = getAuthFileDisplayName(item);
    const tags = getAuthFileTags(item);

    return (
      <tr
        key={item.name}
        className={`${styles.fileTableRow} ${effectiveDisabled ? styles.fileTableRowDisabled : ''}`}
      >
        <td className={styles.fileNameCell}>
          <div className={styles.tableFileMain}>
            <span className={styles.fileName}>{item.name}</span>
            {displayName && displayName !== item.name && (
              <span className={styles.fileDisplayName}>{displayName}</span>
            )}
          </div>
          <div className={styles.tableFileMeta}>
            <span>{item.size ? formatFileSize(item.size) : '-'}</span>
            <span
              className={`${styles.assignmentTag} ${
                assignedApiKeyCount > 0 ? styles.assignmentTagAssigned : styles.assignmentTagUnassigned
              }`}
            >
              {assignedApiKeyCount > 0
                ? t('auth_files.assignment_tag_assigned', { count: assignedApiKeyCount })
                : t('auth_files.assignment_tag_unassigned')}
            </span>
            {tags.map((tag) => (
              <span key={tag} className={styles.tagPill}>
                {tag}
              </span>
            ))}
          </div>
        </td>
        <td>
          <span
            className={styles.typeBadge}
            style={{
              backgroundColor: typeColor.bg,
              color: typeColor.text,
              ...(typeColor.border ? { border: typeColor.border } : {}),
            }}
          >
            {getTypeLabel(item.type || 'unknown')}
          </span>
        </td>
        <td className={styles.quotaCell}>{renderCompactQuota(item)}</td>
        <td className={styles.statusCell}>
          {isRuntimeOnly ? (
            <span className={styles.virtualBadge}>{t('auth_files.type_virtual') || '虚拟认证文件'}</span>
          ) : disabledReason ? (
            <div className={styles.disabledInfoRow}>
              <span className={styles.disabledBadge}>
                {cooldownActive && item.disabled !== true
                  ? t('auth_files.cooldown_badge')
                  : t('common.disabled')}
              </span>
              <span className={styles.disabledReason}>{disabledReason}</span>
            </div>
          ) : (
            <span className={styles.enabledBadge}>{t('common.enabled')}</span>
          )}
        </td>
        <td className={styles.usageCell}>
          <div className={styles.tableStats}>
            <span className={`${styles.statPill} ${styles.statSuccess}`}>{fileStats.success}</span>
            <span className={`${styles.statPill} ${styles.statFailure}`}>{fileStats.failure}</span>
          </div>
          {renderStatusBar(item)}
        </td>
        <td className={styles.timeCell}>
          <div>{formatImportedAt(item)}</div>
          <span>{formatModified(item)}</span>
        </td>
        <td className={styles.actionsCell}>
          <div className={styles.tableActions}>
            {showQuotaRefreshButton && (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => void handleRefreshSingleQuota(item)}
                className={styles.iconButton}
                title={t('common.refresh')}
                disabled={disableControls || quotaRefreshingAll || refreshingSingleQuota}
              >
                {refreshingSingleQuota ? (
                  <LoadingSpinner size={14} />
                ) : (
                  <IconRefreshCw className={styles.actionIcon} size={16} />
                )}
              </Button>
            )}
            {!isRuntimeOnly && isInvalidated401 && (
              <Button
                variant="danger"
                size="sm"
                onClick={() => handleDelete(item.name)}
                className={styles.iconButton}
                title={t('auth_files.delete_401_single_button')}
                disabled={disableControls || deleting401 || deleting === item.name}
              >
                {deleting === item.name ? (
                  <LoadingSpinner size={14} />
                ) : (
                  <IconX className={styles.actionIcon} size={16} />
                )}
              </Button>
            )}
            {showModelsButton && (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => showModels(item)}
                className={styles.iconButton}
                title={t('auth_files.models_button', { defaultValue: '模型' })}
                disabled={disableControls}
              >
                <IconBot className={styles.actionIcon} size={16} />
              </Button>
            )}
            {!isRuntimeOnly && (
              <>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => void openMetadataEditor(item)}
                  className={styles.iconButton}
                  title={t('auth_files.metadata_button')}
                  disabled={disableControls}
                >
                  <IconFileText className={styles.actionIcon} size={16} />
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => showDetails(item)}
                  className={styles.iconButton}
                  title={t('common.info', { defaultValue: '关于' })}
                  disabled={disableControls}
                >
                  <IconInfo className={styles.actionIcon} size={16} />
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => handleDownload(item.name)}
                  className={styles.iconButton}
                  title={t('auth_files.download_button')}
                  disabled={disableControls}
                >
                  <IconDownload className={styles.actionIcon} size={16} />
                </Button>
                <Button
                  variant="danger"
                  size="sm"
                  onClick={() => handleDelete(item.name)}
                  className={styles.iconButton}
                  title={t('auth_files.delete_button')}
                  disabled={disableControls || deleting401 || deleting === item.name}
                >
                  {deleting === item.name ? (
                    <LoadingSpinner size={14} />
                  ) : (
                    <IconTrash2 className={styles.actionIcon} size={16} />
                  )}
                </Button>
                <div className={styles.statusToggle}>
                  <ToggleSwitch
                    ariaLabel={t('auth_files.status_toggle_label')}
                    checked={!effectiveDisabled}
                    disabled={
                      disableControls ||
                      statusUpdating[item.name] === true ||
                      cooldownResetting[item.name] === true
                    }
                    onChange={(value) => void handleStatusToggle(item, value)}
                  />
                </div>
              </>
            )}
          </div>
        </td>
      </tr>
    );
  };
  const titleNode = (
    <div className={styles.titleWrapper}>
      <span>{t('auth_files.title_section')}</span>
      {files.length > 0 && (
        <span
          className={styles.countBadge}
          title={t('auth_files.count_badge_hint', {
            total: files.length,
            enabledWithQuota: enabledWithQuotaCount,
          })}
        >
          {files.length}/{enabledWithQuotaCount}
        </span>
      )}
    </div>
  );

  return (
    <div className={styles.container}>
      <div className={styles.pageHeader}>
        <h1 className={styles.pageTitle}>{t('auth_files.title')}</h1>
        <p className={styles.description}>{t('auth_files.description')}</p>
      </div>

      <Card
        title={titleNode}
        extra={
          <div className={styles.headerActions}>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleHeaderRefresh}
              disabled={loading}
              title={t('auth_files.refresh_data_button')}
            >
              {t('auth_files.refresh_data_button')}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void handleRefreshAllQuotas()}
              disabled={disableControls || quotaRefreshingAll || !hasQuotaTargets}
              loading={quotaRefreshingAll}
              title={t('auth_files.refresh_quota_button')}
            >
              {t('auth_files.refresh_quota_button')}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void handleResetAllCooldowns()}
              disabled={disableControls || resettingAllCooldowns || !hasCooldownTargets}
              loading={resettingAllCooldowns}
              title={t('auth_files.reset_all_cooldowns_button')}
            >
              {t('auth_files.reset_all_cooldowns_button')}
            </Button>
            <div className={styles.uploadMenu} ref={uploadMenuRef}>
              <Button
                size="sm"
                onClick={handleUploadClick}
                disabled={disableControls || uploading}
                loading={uploading}
                className={styles.uploadMenuButton}
                title={t('auth_files.upload_button')}
              >
                <IconUpload className={styles.actionIcon} size={16} />
                <span>{t('auth_files.upload_button')}</span>
                <IconChevronDown className={styles.uploadMenuChevron} size={14} />
              </Button>
              {uploadMenuOpen && (
                <div className={styles.uploadMenuDropdown}>
                  <button type="button" className={styles.uploadMenuItem} onClick={openUploadPasteModal}>
                    <IconClipboard size={16} />
                    <span>{t('auth_files.upload_paste_menu')}</span>
                  </button>
                  <button type="button" className={styles.uploadMenuItem} onClick={openUploadFilePicker}>
                    <IconFileText size={16} />
                    <span>{t('auth_files.upload_file_menu')}</span>
                  </button>
                </div>
              )}
            </div>
            <Button
              variant="danger"
              size="sm"
              onClick={handleDeleteInvalidated401}
              disabled={disableControls || loading || deleting401 || !hasInvalidated401Targets}
              loading={deleting401}
            >
              {t('auth_files.delete_401_button')}
            </Button>
            <Button
              variant="danger"
              size="sm"
              onClick={handleDeleteAll}
              disabled={disableControls || loading || deletingAll || deleting401}
              loading={deletingAll}
            >
              {filter === 'all'
                ? t('auth_files.delete_all_button')
                : `${t('common.delete')} ${getTypeLabel(filter)}`}
            </Button>
            <input
              ref={fileInputRef}
              type="file"
              accept=".json,application/json"
              multiple
              style={{ display: 'none' }}
              onChange={handleFileChange}
            />
          </div>
        }
      >
        {error && <div className={styles.errorBox}>{error}</div>}

        {/* 筛选区域 */}
        <div className={styles.filterSection}>
          {renderFilterTags()}

          <div className={styles.filterControls}>
            <div className={styles.filterItem}>
              <label>{t('auth_files.search_label')}</label>
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t('auth_files.search_placeholder')}
              />
            </div>
          </div>
        </div>

        {/* Auth file list */}
        {loading ? (
          <div className={styles.hint}>{t('common.loading')}</div>
        ) : pageItems.length === 0 ? (
          <EmptyState
            title={t('auth_files.search_empty_title')}
            description={t('auth_files.search_empty_desc')}
          />
        ) : (
          <div className={styles.fileTableWrap}>
            <table className={styles.fileTable}>
              <thead>
                <tr>
                  <th>{t('auth_files.table_file')}</th>
                  <th>{t('auth_files.table_type')}</th>
                  <th>{t('auth_files.table_quota')}</th>
                  <th>{t('auth_files.table_status')}</th>
                  <th>{t('auth_files.table_usage')}</th>
                  <th>{t('auth_files.table_time')}</th>
                  <th>{t('auth_files.table_actions')}</th>
                </tr>
              </thead>
              <tbody>{pageItems.map(renderFileRow)}</tbody>
            </table>
          </div>
        )}
      </Card>

      <Modal
        open={pasteModalOpen}
        onClose={() => {
          if (pasteUploading) return;
          setPasteModalOpen(false);
        }}
        closeDisabled={pasteUploading}
        width={760}
        title={t('auth_files.upload_paste_title')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setPasteModalOpen(false)} disabled={pasteUploading}>
              {t('common.cancel')}
            </Button>
            <Button onClick={() => void handlePasteUpload()} loading={pasteUploading} disabled={disableControls}>
              {t('auth_files.upload_paste_submit')}
            </Button>
          </>
        }
      >
        <div className={styles.formGroup}>
          <label>{t('auth_files.upload_paste_name_label')}</label>
          <Input
            value={pasteFileName}
            onChange={(e) => setPasteFileName(e.target.value)}
            placeholder={t('auth_files.upload_paste_name_placeholder')}
            disabled={pasteUploading}
          />
        </div>
        <div className={styles.formGroup}>
          <label>{t('auth_files.upload_paste_body_label')}</label>
          <textarea
            className={styles.textarea}
            rows={14}
            value={pasteJsonText}
            onChange={(e) => setPasteJsonText(e.target.value)}
            placeholder={t('auth_files.upload_paste_body_placeholder')}
            disabled={pasteUploading}
          />
        </div>
      </Modal>

      <Modal
        open={Boolean(metadataEditor)}
        onClose={() => {
          if (metadataEditor?.saving) return;
          setMetadataEditor(null);
        }}
        closeDisabled={metadataEditor?.saving === true}
        width={840}
        title={t('auth_files.metadata_title')}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => setMetadataEditor(null)}
              disabled={metadataEditor?.saving === true}
            >
              {t('common.cancel')}
            </Button>
            <Button
              onClick={() => void handleMetadataSave()}
              loading={metadataEditor?.saving === true}
              disabled={
                disableControls ||
                metadataEditor?.saving === true ||
                metadataEditor?.loadingFile === true ||
                Boolean(
                  metadataEditor &&
                    metadataEditor.jsonText !== metadataEditor.originalJsonText &&
                    !metadataEditor.json
                )
              }
            >
              {t('common.save')}
            </Button>
          </>
        }
      >
        {metadataEditor && (
          <div className={styles.metadataEditor}>
            <Input
              label={t('auth_files.metadata_file_name')}
              value={metadataEditor.fileName}
              onChange={(e) =>
                setMetadataEditor((prev) =>
                  prev ? { ...prev, fileName: e.target.value } : prev
                )
              }
              disabled={metadataEditor.saving}
            />
            <Input
              label={t('auth_files.metadata_display_name')}
              value={metadataEditor.displayName}
              onChange={(e) =>
                setMetadataEditor((prev) =>
                  prev ? { ...prev, displayName: e.target.value } : prev
                )
              }
              disabled={metadataEditor.saving}
            />
            <div className={styles.formGroup}>
              <label>{t('auth_files.metadata_tags')}</label>
              <textarea
                className={styles.textarea}
                rows={5}
                value={metadataEditor.tagsText}
                onChange={(e) =>
                  setMetadataEditor((prev) => (prev ? { ...prev, tagsText: e.target.value } : prev))
                }
                placeholder={t('auth_files.metadata_tags_placeholder')}
                disabled={metadataEditor.saving}
              />
              <div className={styles.hint}>{t('auth_files.metadata_tags_hint')}</div>
            </div>
            <div className={styles.prefixProxyJsonWrapper}>
              <label className={styles.prefixProxyLabel}>
                {t('auth_files.prefix_proxy_source_label')}
              </label>
              {metadataEditor.loadingFile ? (
                <div className={styles.prefixProxyLoading}>
                  <LoadingSpinner size={14} />
                  <span>{t('auth_files.prefix_proxy_loading')}</span>
                </div>
              ) : (
                <>
                  {metadataEditor.fileError && (
                    <div className={styles.prefixProxyError}>{metadataEditor.fileError}</div>
                  )}
                  <textarea
                    className={styles.prefixProxyTextarea}
                    rows={16}
                    value={metadataEditor.jsonText}
                    onChange={(e) => handleMetadataJsonChange(e.target.value)}
                    disabled={metadataEditor.saving}
                  />
                </>
              )}
            </div>
          </div>
        )}
      </Modal>

      {/* OAuth 排除列表卡片 */}
      <Card
        title={t('oauth_excluded.title')}
        extra={
          <Button
            size="sm"
            onClick={() => openExcludedModal()}
            disabled={disableControls || excludedError === 'unsupported'}
          >
            {t('oauth_excluded.add')}
          </Button>
        }
      >
        {excludedError === 'unsupported' ? (
          <EmptyState
            title={t('oauth_excluded.upgrade_required_title')}
            description={t('oauth_excluded.upgrade_required_desc')}
          />
        ) : Object.keys(excluded).length === 0 ? (
          <EmptyState title={t('oauth_excluded.list_empty_all')} />
        ) : (
          <div className={styles.excludedList}>
            {Object.entries(excluded).map(([provider, models]) => (
              <div key={provider} className={styles.excludedItem}>
                <div className={styles.excludedInfo}>
                  <div className={styles.excludedProvider}>{provider}</div>
                  <div className={styles.excludedModels}>
                    {models?.length
                      ? t('oauth_excluded.model_count', { count: models.length })
                      : t('oauth_excluded.no_models')}
                  </div>
                </div>
                <div className={styles.excludedActions}>
                  <Button variant="secondary" size="sm" onClick={() => openExcludedModal(provider)}>
                    {t('common.edit')}
                  </Button>
                  <Button variant="danger" size="sm" onClick={() => deleteExcluded(provider)}>
                    {t('oauth_excluded.delete')}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* OAuth 模型映射卡片 */}
      <Card
        title={t('oauth_model_mappings.title')}
        extra={
          <Button
            size="sm"
            onClick={() => openMappingsModal()}
            disabled={disableControls || modelMappingsError === 'unsupported'}
          >
            {t('oauth_model_mappings.add')}
          </Button>
        }
      >
        {modelMappingsError === 'unsupported' ? (
          <EmptyState
            title={t('oauth_model_mappings.upgrade_required_title')}
            description={t('oauth_model_mappings.upgrade_required_desc')}
          />
        ) : Object.keys(modelMappings).length === 0 ? (
          <EmptyState title={t('oauth_model_mappings.list_empty_all')} />
        ) : (
          <div className={styles.excludedList}>
            {Object.entries(modelMappings).map(([provider, mappings]) => (
              <div key={provider} className={styles.excludedItem}>
                <div className={styles.excludedInfo}>
                  <div className={styles.excludedProvider}>{provider}</div>
                  <div className={styles.excludedModels}>
                    {mappings?.length
                      ? t('oauth_model_mappings.model_count', { count: mappings.length })
                      : t('oauth_model_mappings.no_models')}
                  </div>
                </div>
                <div className={styles.excludedActions}>
                  <Button variant="secondary" size="sm" onClick={() => openMappingsModal(provider)}>
                    {t('common.edit')}
                  </Button>
                  <Button variant="danger" size="sm" onClick={() => deleteModelMappings(provider)}>
                    {t('oauth_model_mappings.delete')}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* 详情弹窗 */}
      <Modal
        open={detailModalOpen}
        onClose={() => setDetailModalOpen(false)}
        title={selectedFile?.name || t('auth_files.title_section')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDetailModalOpen(false)}>
              {t('common.close')}
            </Button>
            <Button
              onClick={() => {
                if (selectedFile) {
                  const text = JSON.stringify(selectedFile, null, 2);
                  navigator.clipboard.writeText(text).then(() => {
                    showNotification(t('notification.link_copied'), 'success');
                  });
                }
              }}
            >
              {t('common.copy')}
            </Button>
          </>
        }
      >
        {selectedFile && (
          <div className={styles.detailContent}>
            <pre className={styles.jsonContent}>{JSON.stringify(selectedFile, null, 2)}</pre>
          </div>
        )}
      </Modal>

      {/* 模型列表弹窗 */}
      <Modal
        open={modelsModalOpen}
        onClose={() => setModelsModalOpen(false)}
        title={
          t('auth_files.models_title', { defaultValue: '支持的模型' }) + ` - ${modelsFileName}`
        }
        footer={
          <Button variant="secondary" onClick={() => setModelsModalOpen(false)}>
            {t('common.close')}
          </Button>
        }
      >
        {modelsLoading ? (
          <div className={styles.hint}>
            {t('auth_files.models_loading', { defaultValue: '正在加载模型列表...' })}
          </div>
        ) : modelsError === 'unsupported' ? (
          <EmptyState
            title={t('auth_files.models_unsupported', { defaultValue: '当前版本不支持此功能' })}
            description={t('auth_files.models_unsupported_desc', {
              defaultValue: '请更新 CLI Proxy API 到最新版本后重试',
            })}
          />
        ) : modelsList.length === 0 ? (
          <EmptyState
            title={t('auth_files.models_empty', { defaultValue: '该凭证暂无可用模型' })}
            description={t('auth_files.models_empty_desc', {
              defaultValue: '该认证凭证可能尚未被服务器加载或没有绑定任何模型',
            })}
          />
        ) : (
          <div className={styles.modelsList}>
            {modelsList.map((model) => {
              const isExcluded = isModelExcluded(model.id, modelsFileType);
              return (
                <div
                  key={model.id}
                  className={`${styles.modelItem} ${isExcluded ? styles.modelItemExcluded : ''}`}
                  onClick={() => {
                    navigator.clipboard.writeText(model.id);
                    showNotification(
                      t('notification.link_copied', { defaultValue: '已复制到剪贴板' }),
                      'success'
                    );
                  }}
                  title={
                    isExcluded
                      ? t('auth_files.models_excluded_hint', {
                          defaultValue: '此模型已被 OAuth 排除',
                        })
                      : t('common.copy', { defaultValue: '点击复制' })
                  }
                >
                  <span className={styles.modelId}>{model.id}</span>
                  {model.display_name && model.display_name !== model.id && (
                    <span className={styles.modelDisplayName}>{model.display_name}</span>
                  )}
                  {model.type && <span className={styles.modelType}>{model.type}</span>}
                  {isExcluded && (
                    <span className={styles.modelExcludedBadge}>
                      {t('auth_files.models_excluded_badge', { defaultValue: '已排除' })}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Modal>

      {/* OAuth 排除弹窗 */}
      <Modal
        open={excludedModalOpen}
        onClose={() => setExcludedModalOpen(false)}
        title={t('oauth_excluded.add_title')}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => setExcludedModalOpen(false)}
              disabled={savingExcluded}
            >
              {t('common.cancel')}
            </Button>
            <Button onClick={saveExcludedModels} loading={savingExcluded}>
              {t('oauth_excluded.save')}
            </Button>
          </>
        }
      >
        <div className={styles.providerField}>
          <AutocompleteInput
            id="oauth-excluded-provider"
            label={t('oauth_excluded.provider_label')}
            hint={t('oauth_excluded.provider_hint')}
            placeholder={t('oauth_excluded.provider_placeholder')}
            value={excludedForm.provider}
            onChange={(val) => setExcludedForm((prev) => ({ ...prev, provider: val }))}
            options={providerOptions}
          />
          {providerOptions.length > 0 && (
            <div className={styles.providerTagList}>
              {providerOptions.map((provider) => {
                const isActive =
                  excludedForm.provider.trim().toLowerCase() === provider.toLowerCase();
                return (
                  <button
                    key={provider}
                    type="button"
                    className={`${styles.providerTag} ${isActive ? styles.providerTagActive : ''}`}
                    onClick={() => setExcludedForm((prev) => ({ ...prev, provider }))}
                    disabled={savingExcluded}
                  >
                    {getTypeLabel(provider)}
                  </button>
                );
              })}
            </div>
          )}
        </div>
        <div className={styles.formGroup}>
          <label>{t('oauth_excluded.models_label')}</label>
          <textarea
            className={styles.textarea}
            rows={4}
            placeholder={t('oauth_excluded.models_placeholder')}
            value={excludedForm.modelsText}
            onChange={(e) => setExcludedForm((prev) => ({ ...prev, modelsText: e.target.value }))}
          />
          <div className={styles.hint}>{t('oauth_excluded.models_hint')}</div>
        </div>
      </Modal>

      {/* OAuth 模型映射弹窗 */}
      <Modal
        open={mappingModalOpen}
        onClose={() => setMappingModalOpen(false)}
        title={t('oauth_model_mappings.add_title')}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => setMappingModalOpen(false)}
              disabled={savingMappings}
            >
              {t('common.cancel')}
            </Button>
            <Button onClick={saveModelMappings} loading={savingMappings}>
              {t('oauth_model_mappings.save')}
            </Button>
          </>
        }
      >
        <div className={styles.providerField}>
          <AutocompleteInput
            id="oauth-model-alias-provider"
            label={t('oauth_model_mappings.provider_label')}
            hint={t('oauth_model_mappings.provider_hint')}
            placeholder={t('oauth_model_mappings.provider_placeholder')}
            value={mappingForm.provider}
            onChange={(val) => setMappingForm((prev) => ({ ...prev, provider: val }))}
            options={providerOptions}
          />
          {providerOptions.length > 0 && (
            <div className={styles.providerTagList}>
              {providerOptions.map((provider) => {
                const isActive =
                  mappingForm.provider.trim().toLowerCase() === provider.toLowerCase();
                return (
                  <button
                    key={provider}
                    type="button"
                    className={`${styles.providerTag} ${isActive ? styles.providerTagActive : ''}`}
                    onClick={() => setMappingForm((prev) => ({ ...prev, provider }))}
                    disabled={savingMappings}
                  >
                    {getTypeLabel(provider)}
                  </button>
                );
              })}
            </div>
          )}
        </div>
        <div className={styles.providerField}>
          <AutocompleteInput
            id="oauth-model-mapping-model-source"
            label={t('oauth_model_mappings.model_source_label')}
            hint={
              mappingModelsLoading
                ? t('oauth_model_mappings.model_source_loading')
                : mappingModelsError === 'unsupported'
                  ? t('oauth_model_mappings.model_source_unsupported')
                  : !mappingModelsFileName.trim()
                    ? t('oauth_model_mappings.model_source_hint')
                    : t('oauth_model_mappings.model_source_loaded', {
                        count: mappingModelsList.length,
                      })
            }
            placeholder={t('oauth_model_mappings.model_source_placeholder')}
            value={mappingModelsFileName}
            onChange={(val) => setMappingModelsFileName(val)}
            disabled={savingMappings}
            options={modelSourceFileOptions}
          />
        </div>
        <div className={styles.formGroup}>
          <label>{t('oauth_model_mappings.mappings_label')}</label>
          <div className="header-input-list">
            {(mappingForm.mappings.length ? mappingForm.mappings : [buildEmptyMappingEntry()]).map(
              (entry, index) => (
                <div key={entry.id} className={styles.mappingRow}>
                  <AutocompleteInput
                    wrapperStyle={{ flex: 1, marginBottom: 0 }}
                    placeholder={t('oauth_model_mappings.mapping_name_placeholder')}
                    value={entry.name}
                    onChange={(val) => updateMappingEntry(index, 'name', val)}
                    disabled={savingMappings}
                    options={mappingModelsList.map((m) => ({
                      value: m.id,
                      label: m.display_name && m.display_name !== m.id ? m.display_name : undefined,
                    }))}
                  />
                  <span className={styles.mappingSeparator}>→</span>
                  <input
                    className="input"
                    placeholder={t('oauth_model_mappings.mapping_alias_placeholder')}
                    value={entry.alias}
                    onChange={(e) => updateMappingEntry(index, 'alias', e.target.value)}
                    disabled={savingMappings}
                    style={{ flex: 1 }}
                  />
                  <div className={styles.mappingFork}>
                    <ToggleSwitch
                      label={t('oauth_model_mappings.mapping_fork_label')}
                      labelPosition="left"
                      checked={Boolean(entry.fork)}
                      onChange={(value) => updateMappingEntry(index, 'fork', value)}
                      disabled={savingMappings}
                    />
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => removeMappingEntry(index)}
                    disabled={savingMappings || mappingForm.mappings.length <= 1}
                    title={t('common.delete')}
                    aria-label={t('common.delete')}
                  >
                    <IconX size={14} />
                  </Button>
                </div>
              )
            )}
            <Button
              variant="secondary"
              size="sm"
              onClick={addMappingEntry}
              disabled={savingMappings}
              className="align-start"
            >
              {t('oauth_model_mappings.add_mapping')}
            </Button>
          </div>
          <div className={styles.hint}>{t('oauth_model_mappings.mappings_hint')}</div>
        </div>
      </Modal>
    </div>
  );
}
