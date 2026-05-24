/**
 * 认证文件相关类型
 * 基于原项目 src/modules/auth-files.js
 */

export type AuthFileType =
  | 'qwen'
  | 'gemini'
  | 'gemini-cli'
  | 'aistudio'
  | 'claude'
  | 'codex'
  | 'antigravity'
  | 'iflow'
  | 'vertex'
  | 'empty'
  | 'unknown';

export interface AuthFileItem {
  name: string;
  type?: AuthFileType | string;
  provider?: string;
  size?: number;
  authIndex?: string | number | null;
  runtimeOnly?: boolean | string;
  disabled?: boolean;
  disabledEffective?: boolean;
  disabledReason?: string;
  cooldownActive?: boolean;
  cooldownReason?: string;
  cooldownUntil?: string | number | Date;
  modified?: number;
  imported_at?: string | number | Date;
  importedAt?: string | number | Date;
  imported_at_source?: string;
  display_name?: string;
  displayName?: string;
  tags?: string[];
  [key: string]: any;
}

export interface AuthFilesResponse {
  files: AuthFileItem[];
  total?: number;
}
