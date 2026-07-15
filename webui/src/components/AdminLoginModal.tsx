import { useEffect, useRef, useState } from 'react';
import { LogIn, X } from 'lucide-react';

type Props = {
  open: boolean;
  busy: boolean;
  onClose: () => void;
  onLogin: (username: string, password: string) => Promise<void>;
};

export function AdminLoginModal({ open, busy, onClose, onLogin }: Props) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const usernameRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) {
      setPassword('');
      setError('');
      return;
    }
    usernameRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) {
        setPassword('');
        onClose();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [busy, onClose, open]);

  if (!open) return null;

  return (
    <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && !busy && onClose()}>
      <section className="login-modal" role="dialog" aria-modal="true" aria-labelledby="login-title">
        <button className="modal-close" type="button" aria-label="关闭登录" disabled={busy} onClick={onClose}>
          <X size={18} />
        </button>
        <p className="eyebrow">PalRest 控制台</p>
        <h2 id="login-title">管理员登录</h2>
        <p className="modal-copy">登录后可编辑策略并重置玩家状态。</p>
        <form
          className="credential-form"
          onSubmit={(event) => {
            event.preventDefault();
            setError('');
            void onLogin(username, password)
              .then(() => {
                setPassword('');
                onClose();
              })
              .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : '登录失败'));
          }}
        >
          <label htmlFor="admin-username">用户名</label>
          <input ref={usernameRef} id="admin-username" name="username" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required />
          <label htmlFor="admin-password">密码</label>
          <input id="admin-password" name="password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required />
          {error && <p className="form-error" role="alert">{error}</p>}
          <button className="primary-button" type="submit" disabled={busy}>
            <LogIn size={17} />
            {busy ? '登录中…' : '登录'}
          </button>
        </form>
      </section>
    </div>
  );
}
