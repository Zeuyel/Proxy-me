import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { authFilesApi, codexClientProfilesApi, type CodexClientProfile } from '@/services/api';
import { useNotificationStore } from '@/stores';
import type { AuthFileItem } from '@/types/authFile';
import styles from './CodexClientProfilesPage.module.scss';

export function CodexClientProfilesPage() {
  const { showNotification } = useNotificationStore();
  const [profiles, setProfiles] = useState<CodexClientProfile[]>([]);
  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [name, setName] = useState('');
  const [userAgent, setUserAgent] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    void Promise.all([codexClientProfilesApi.list(), authFilesApi.list()])
      .then(([nextProfiles, response]) => {
        const codexFiles = (response.files || []).filter((file) => file.type === 'codex');
        setProfiles(nextProfiles);
        setFiles(codexFiles);
        setSelected(Object.fromEntries(codexFiles.map((file) => [file.name, String(file.client_profile || '')])));
      })
      .catch((error: unknown) => showNotification(String(error), 'error'))
      .finally(() => setLoading(false));
  }, [showNotification]);

  const saveProfiles = async (next: CodexClientProfile[]) => {
    setSaving(true);
    try {
      setProfiles(await codexClientProfilesApi.save(next));
    } catch (error: unknown) {
      showNotification(String(error), 'error');
    } finally {
      setSaving(false);
    }
  };

  const addProfile = async () => {
    const profileName = name.trim();
    const profileUserAgent = userAgent.trim();
    if (!profileName || !profileUserAgent) return;
    await saveProfiles([...profiles, {
      id: crypto.randomUUID(),
      name: profileName,
      headers: { user_agent: profileUserAgent }
    }]);
    setName('');
    setUserAgent('');
  };

  const removeProfile = async (profileID: string) => {
    const boundFiles = Object.entries(selected).filter(([, value]) => value === profileID);
    if (boundFiles.length > 0) {
      showNotification('请先解除认证文件绑定', 'error');
      return;
    }
    await saveProfiles(profiles.filter((profile) => profile.id !== profileID));
  };

  const bindProfile = async (file: AuthFileItem, profileID: string) => {
    setSaving(true);
    try {
      await authFilesApi.patchFields(file.name, { client_profile: profileID });
      setSelected((current) => ({ ...current, [file.name]: profileID }));
    } catch (error: unknown) {
      showNotification(String(error), 'error');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className={styles.container}>
      <h1 className={styles.title}>Codex 客户端身份</h1>
      <div className={styles.content}>
        <Card title="预设">
          <div className={styles.profileList}>
            {profiles.map((profile) => (
              <div className={styles.profileRow} key={profile.id}>
                <div>
                  <div className={styles.profileName}>{profile.name}</div>
                  <div className={styles.profileAgent}>{profile.headers.user_agent || profile.headers['User-Agent']}</div>
                </div>
                <Button variant="danger" size="sm" onClick={() => void removeProfile(profile.id)} disabled={saving}>
                  删除
                </Button>
              </div>
            ))}
          </div>
          <div className={styles.createForm}>
            <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="名称" />
            <Input value={userAgent} onChange={(event) => setUserAgent(event.target.value)} placeholder="User-Agent" />
            <Button onClick={() => void addProfile()} loading={saving} disabled={!name.trim() || !userAgent.trim()}>
              新增
            </Button>
          </div>
        </Card>

        <Card title="认证文件">
          {loading ? <div className="hint">加载中</div> : (
            <div className={styles.fileList}>
              {files.map((file) => (
                <label className={styles.fileRow} key={file.name}>
                  <span className={styles.fileName}>{file.display_name || file.name}</span>
                  <select value={selected[file.name] || ''} onChange={(event) => void bindProfile(file, event.target.value)} disabled={saving}>
                    <option value="">默认</option>
                    {profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}
                  </select>
                </label>
              ))}
              {!files.length && <div className="hint">没有 Codex 认证文件</div>}
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
