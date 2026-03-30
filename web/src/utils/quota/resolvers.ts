/**
 * Resolver functions for extracting data from auth files.
 */

import type { AuthFileItem } from '@/types';
import {
  normalizeStringValue,
  normalizePlanType,
  parseIdTokenPayload
} from './parsers';

function resolveCodexAccountIdFromPayload(payload: Record<string, unknown> | null): string | null {
  if (!payload) return null;

  const directCandidates = [
    payload.account_id,
    payload.accountId,
    payload.chatgpt_account_id,
    payload.chatgptAccountId
  ];

  for (const candidate of directCandidates) {
    const value = normalizeStringValue(candidate);
    if (value) return value;
  }

  const authNamespace =
    payload['https://api.openai.com/auth'] &&
    typeof payload['https://api.openai.com/auth'] === 'object' &&
    !Array.isArray(payload['https://api.openai.com/auth'])
      ? (payload['https://api.openai.com/auth'] as Record<string, unknown>)
      : null;

  if (!authNamespace) return null;

  for (const candidate of [
    authNamespace.account_id,
    authNamespace.accountId,
    authNamespace.chatgpt_account_id,
    authNamespace.chatgptAccountId
  ]) {
    const value = normalizeStringValue(candidate);
    if (value) return value;
  }

  return null;
}

export function extractCodexChatgptAccountId(value: unknown): string | null {
  if (!value) return null;

  if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
    const direct = resolveCodexAccountIdFromPayload(value as Record<string, unknown>);
    if (direct) return direct;
  }

  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) return null;
    if (!trimmed.includes('.')) {
      return normalizeStringValue(trimmed);
    }
  }

  const payload = parseIdTokenPayload(value);
  return resolveCodexAccountIdFromPayload(payload);
}

export function resolveCodexChatgptAccountId(file: AuthFileItem): string | null {
  const metadata =
    file && typeof file.metadata === 'object' && file.metadata !== null
      ? (file.metadata as Record<string, unknown>)
      : null;
  const attributes =
    file && typeof file.attributes === 'object' && file.attributes !== null
      ? (file.attributes as Record<string, unknown>)
      : null;

  const candidates = [
    file.account_id,
    file.accountId,
    file.chatgpt_account_id,
    file.chatgptAccountId,
    file.access_token,
    file.id_token,
    file.token,
    metadata?.account_id,
    metadata?.accountId,
    metadata?.chatgpt_account_id,
    metadata?.chatgptAccountId,
    metadata?.access_token,
    metadata?.id_token,
    metadata?.token,
    attributes?.account_id,
    attributes?.accountId,
    attributes?.chatgpt_account_id,
    attributes?.chatgptAccountId,
    attributes?.access_token,
    attributes?.id_token,
    attributes?.token
  ];

  for (const candidate of candidates) {
    const id = extractCodexChatgptAccountId(candidate);
    if (id) return id;
  }

  return null;
}

export function resolveCodexPlanType(file: AuthFileItem): string | null {
  const metadata =
    file && typeof file.metadata === 'object' && file.metadata !== null
      ? (file.metadata as Record<string, unknown>)
      : null;
  const attributes =
    file && typeof file.attributes === 'object' && file.attributes !== null
      ? (file.attributes as Record<string, unknown>)
      : null;
  const idToken =
    file && typeof file.id_token === 'object' && file.id_token !== null
      ? (file.id_token as Record<string, unknown>)
      : null;
  const metadataIdToken =
    metadata && typeof metadata.id_token === 'object' && metadata.id_token !== null
      ? (metadata.id_token as Record<string, unknown>)
      : null;
  const candidates = [
    file.plan_type,
    file.planType,
    file['plan_type'],
    file['planType'],
    file.id_token,
    idToken?.plan_type,
    idToken?.planType,
    metadata?.plan_type,
    metadata?.planType,
    metadata?.id_token,
    metadataIdToken?.plan_type,
    metadataIdToken?.planType,
    attributes?.plan_type,
    attributes?.planType,
    attributes?.id_token
  ];

  for (const candidate of candidates) {
    const planType = normalizePlanType(candidate);
    if (planType) return planType;
  }

  return null;
}

export function extractGeminiCliProjectId(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const matches = Array.from(value.matchAll(/\(([^()]+)\)/g));
  if (matches.length === 0) return null;
  const candidate = matches[matches.length - 1]?.[1]?.trim();
  return candidate ? candidate : null;
}

export function resolveGeminiCliProjectId(file: AuthFileItem): string | null {
  const metadata =
    file && typeof file.metadata === 'object' && file.metadata !== null
      ? (file.metadata as Record<string, unknown>)
      : null;
  const attributes =
    file && typeof file.attributes === 'object' && file.attributes !== null
      ? (file.attributes as Record<string, unknown>)
      : null;

  const candidates = [
    file.account,
    file['account'],
    metadata?.account,
    attributes?.account
  ];

  for (const candidate of candidates) {
    const projectId = extractGeminiCliProjectId(candidate);
    if (projectId) return projectId;
  }

  return null;
}
