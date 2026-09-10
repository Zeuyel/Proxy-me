import { apiClient } from './client';

export type CodexClientProfile = { id: string; name: string; headers: Record<string, string> };

export const codexClientProfilesApi = {
  async list(): Promise<CodexClientProfile[]> {
    const data = await apiClient.get('/codex-client-profiles');
    return data.profiles || [];
  },
  async save(profiles: CodexClientProfile[]): Promise<CodexClientProfile[]> {
    const data = await apiClient.put('/codex-client-profiles', profiles);
    return data.profiles || [];
  },
};
