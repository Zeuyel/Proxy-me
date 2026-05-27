/**
 * Validation and type checking functions for quota management.
 */

import type { AuthFileItem } from '@/types';
import { GEMINI_CLI_IGNORED_MODEL_PREFIXES } from './constants';

export function resolveAuthProvider(file: AuthFileItem): string {
  const raw = file.provider ?? file.type ?? '';
  return String(raw).trim().toLowerCase();
}

function hasAuthProviderOrType(file: AuthFileItem, expected: string): boolean {
  const normalized = expected.trim().toLowerCase();
  const candidates = [file.provider, file.type];
  return candidates.some((value) => String(value ?? '').trim().toLowerCase() === normalized);
}

export function isAntigravityFile(file: AuthFileItem): boolean {
  return hasAuthProviderOrType(file, 'antigravity');
}

export function isCodexFile(file: AuthFileItem): boolean {
  return hasAuthProviderOrType(file, 'codex');
}

export function isGeminiCliFile(file: AuthFileItem): boolean {
  return hasAuthProviderOrType(file, 'gemini-cli');
}

export function isRuntimeOnlyAuthFile(file: AuthFileItem): boolean {
  const raw = file['runtime_only'] ?? file.runtimeOnly;
  if (typeof raw === 'boolean') return raw;
  if (typeof raw === 'string') return raw.trim().toLowerCase() === 'true';
  return false;
}

export function isIgnoredGeminiCliModel(modelId: string): boolean {
  return GEMINI_CLI_IGNORED_MODEL_PREFIXES.some(
    (prefix) => modelId === prefix || modelId.startsWith(`${prefix}-`)
  );
}
