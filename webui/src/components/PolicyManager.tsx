import { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, Database, Plus, Save, Search, Trash2, X } from 'lucide-react';
import type { Player, Policies, PolicyDocument } from '../api';
import { RuleForm } from './RuleForm';

type Props = {
  policies: Policies;
  players: Player[];
  busy: boolean;
  onSave: (policy: PolicyDocument) => Promise<void>;
  onBack: () => void;
};

const globalKey = '__global__';

export function PolicyManager({ policies, players, busy, onSave, onBack }: Props) {
  const [draft, setDraft] = useState(() => clonePolicy(policies));
  const [selected, setSelected] = useState(globalKey);
  const [dirty, setDirty] = useState(false);
  const [adding, setAdding] = useState(false);
  const [query, setQuery] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    if (!dirty) setDraft(clonePolicy(policies));
  }, [dirty, policies]);

  const names = useMemo(() => new Map(players.map((player) => [player.user_id, player.name || player.account_name || player.user_id])), [players]);
  const overrides = Object.keys(draft.overrides).filter((id) => `${names.get(id) ?? ''} ${id}`.toLowerCase().includes(query.toLowerCase()));
  const choose = (key: string) => {
    if (dirty && key !== selected && !window.confirm('放弃未保存的策略更改？')) return;
    if (key !== selected) setDirty(false);
    setSelected(key);
  };
  const document: PolicyDocument = { timezone: draft.timezone, default: draft.default, overrides: draft.overrides };

  return <section className="policy-workspace">
    <header className="policy-toolbar">
      <div><button className="back-button" type="button" onClick={onBack}><ArrowLeft size={17} />返回总览</button><h2>策略管理</h2><p><Database size={14} /> 当前数据源：SQLite</p></div>
      <button className="primary-button save-policy" type="button" disabled={busy || !dirty} onClick={() => {
        setError('');
        void onSave(document).then(() => setDirty(false)).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : '无法保存策略'));
      }}><Save size={17} />{busy ? '保存中…' : '保存策略'}</button>
    </header>
    {error && <div className="notice" role="alert">{error}</div>}
    <div className={`policy-layout ${selected === globalKey ? 'show-master-mobile' : 'show-detail-mobile'}`}>
      <aside className="policy-master">
        <button className={`policy-item ${selected === globalKey ? 'active' : ''}`} type="button" onClick={() => choose(globalKey)}><span className="policy-avatar">全</span><span><strong>全局默认</strong><small>无覆盖规则的所有玩家</small></span></button>
        <label className="policy-search"><Search size={16} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索覆盖规则" aria-label="搜索覆盖规则" /></label>
        <div className="policy-items">{overrides.map((id) => <button className={`policy-item ${selected === id ? 'active' : ''}`} type="button" key={id} onClick={() => choose(id)}><span className="policy-avatar">{(names.get(id) ?? id).slice(0, 2).toUpperCase()}</span><span><strong>{names.get(id) ?? id}</strong><small>{id}</small></span></button>)}</div>
        <button className="add-override" type="button" onClick={() => setAdding(true)}><Plus size={17} />添加覆盖</button>
      </aside>
      <article className="policy-detail">
        {selected !== globalKey && <button className="mobile-master-back" type="button" onClick={() => choose(globalKey)}><ArrowLeft size={16} />全部策略</button>}
        {selected === globalKey ? <>
          <div className="detail-heading"><div><span className="label">全局默认</span><h3>默认游玩时长策略</h3><p>适用于没有单独覆盖规则的每一位玩家。</p></div></div>
          <RuleForm kind="global" timezone={draft.timezone} rule={draft.default} onTimezone={(timezone) => { setDraft({ ...draft, timezone }); setDirty(true); }} onChange={(rule) => { setDraft({ ...draft, default: rule }); setDirty(true); }} />
        </> : draft.overrides[selected] ? <>
          <div className="detail-heading"><div><span className="label">玩家覆盖</span><h3>{names.get(selected) ?? selected}</h3><p>{selected}</p></div><button className="danger-button" type="button" onClick={() => {
            if (!window.confirm(`删除 ${selected} 的覆盖规则？`)) return;
            const next = { ...draft.overrides }; delete next[selected];
            setDraft({ ...draft, overrides: next }); setSelected(globalKey); setDirty(true);
          }}><Trash2 size={16} />删除</button></div>
          <RuleForm kind="override" rule={draft.overrides[selected]} defaults={draft.default} onChange={(rule) => { setDraft({ ...draft, overrides: { ...draft.overrides, [selected]: rule } }); setDirty(true); }} />
        </> : null}
      </article>
    </div>
    {adding && <AddOverrideDialog players={players} existing={draft.overrides} onClose={() => setAdding(false)} onCreate={(id) => {
      setDraft({ ...draft, overrides: { ...draft.overrides, [id]: { exempt: false } } });
      setSelected(id); setDirty(true); setAdding(false);
    }} />}
  </section>;
}

function AddOverrideDialog({ players, existing, onClose, onCreate }: { players: Player[]; existing: Policies['overrides']; onClose: () => void; onCreate: (id: string) => void }) {
  const available = players.filter((player) => !existing[player.user_id]);
  const [mode, setMode] = useState<'known' | 'manual'>('known');
  const [known, setKnown] = useState(available[0]?.user_id ?? '');
  const [manual, setManual] = useState('');
  const [error, setError] = useState('');
  const submit = () => {
    const id = (mode === 'known' ? known : manual).trim();
    if (!id) return setError('需要用户 ID');
    if (existing[id]) return setError('该玩家已有覆盖规则');
    onCreate(id);
  };
  return <div className="modal-backdrop"><section className="add-dialog" role="dialog" aria-modal="true" aria-labelledby="add-title"><button className="modal-close" aria-label="关闭添加覆盖" type="button" onClick={onClose}><X size={18} /></button><h2 id="add-title">添加玩家覆盖</h2><div className="mode-choice"><label><input aria-label="选择已知玩家" type="radio" checked={mode === 'known'} onChange={() => setMode('known')} />已知玩家</label><label><input aria-label="手动输入用户 ID" type="radio" checked={mode === 'manual'} onChange={() => setMode('manual')} />手动用户 ID</label></div>{mode === 'known' ? <label className="field"><span>已知玩家</span><select value={known} onChange={(event) => setKnown(event.target.value)}>{available.map((player) => <option value={player.user_id} key={player.user_id}>{player.name || player.account_name || player.user_id}</option>)}</select></label> : <label className="field"><span>用户 ID</span><input value={manual} onChange={(event) => setManual(event.target.value)} /></label>}{error && <p className="form-error" role="alert">{error}</p>}<button className="primary-button" type="button" onClick={submit}>创建覆盖</button></section></div>;
}

function clonePolicy(policy: Policies): Policies {
  return structuredClone(policy);
}
