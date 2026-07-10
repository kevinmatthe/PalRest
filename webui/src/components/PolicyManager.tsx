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
    if (dirty && key !== selected && !window.confirm('Discard unsaved policy changes?')) return;
    if (key !== selected) setDirty(false);
    setSelected(key);
  };
  const document: PolicyDocument = { timezone: draft.timezone, default: draft.default, overrides: draft.overrides };

  return <section className="policy-workspace">
    <header className="policy-toolbar">
      <div><button className="back-button" type="button" onClick={onBack}><ArrowLeft size={17} />Dashboard</button><h2>Policy management</h2><p><Database size={14} /> SQLite is the active source</p></div>
      <button className="primary-button save-policy" type="button" disabled={busy || !dirty} onClick={() => {
        setError('');
        void onSave(document).then(() => setDirty(false)).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : 'Could not save policy'));
      }}><Save size={17} />{busy ? 'Saving…' : 'Save policy'}</button>
    </header>
    {error && <div className="notice" role="alert">{error}</div>}
    <div className={`policy-layout ${selected === globalKey ? 'show-master-mobile' : 'show-detail-mobile'}`}>
      <aside className="policy-master">
        <button className={`policy-item ${selected === globalKey ? 'active' : ''}`} type="button" onClick={() => choose(globalKey)}><span className="policy-avatar">G</span><span><strong>Global default</strong><small>All players without an override</small></span></button>
        <label className="policy-search"><Search size={16} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search overrides" /></label>
        <div className="policy-items">{overrides.map((id) => <button className={`policy-item ${selected === id ? 'active' : ''}`} type="button" key={id} onClick={() => choose(id)}><span className="policy-avatar">{(names.get(id) ?? id).slice(0, 2).toUpperCase()}</span><span><strong>{names.get(id) ?? id}</strong><small>{id}</small></span></button>)}</div>
        <button className="add-override" type="button" onClick={() => setAdding(true)}><Plus size={17} />Add override</button>
      </aside>
      <article className="policy-detail">
        {selected !== globalKey && <button className="mobile-master-back" type="button" onClick={() => choose(globalKey)}><ArrowLeft size={16} />All policies</button>}
        {selected === globalKey ? <>
          <div className="detail-heading"><div><span className="label">Global default</span><h3>Default playtime policy</h3><p>Used by every player without a specific override.</p></div></div>
          <RuleForm kind="global" timezone={draft.timezone} rule={draft.default} onTimezone={(timezone) => { setDraft({ ...draft, timezone }); setDirty(true); }} onChange={(rule) => { setDraft({ ...draft, default: rule }); setDirty(true); }} />
        </> : draft.overrides[selected] ? <>
          <div className="detail-heading"><div><span className="label">Player override</span><h3>{names.get(selected) ?? selected}</h3><p>{selected}</p></div><button className="danger-button" type="button" onClick={() => {
            if (!window.confirm(`Delete override for ${selected}?`)) return;
            const next = { ...draft.overrides }; delete next[selected];
            setDraft({ ...draft, overrides: next }); setSelected(globalKey); setDirty(true);
          }}><Trash2 size={16} />Delete</button></div>
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
    if (!id) return setError('User ID is required');
    if (existing[id]) return setError('This player already has an override');
    onCreate(id);
  };
  return <div className="modal-backdrop"><section className="add-dialog" role="dialog" aria-modal="true" aria-labelledby="add-title"><button className="modal-close" aria-label="Close add override" type="button" onClick={onClose}><X size={18} /></button><h2 id="add-title">Add player override</h2><div className="mode-choice"><label><input aria-label="Choose known player" type="radio" checked={mode === 'known'} onChange={() => setMode('known')} />Known player</label><label><input aria-label="Manual User ID" type="radio" checked={mode === 'manual'} onChange={() => setMode('manual')} />Manual User ID</label></div>{mode === 'known' ? <label className="field"><span>Known player</span><select value={known} onChange={(event) => setKnown(event.target.value)}>{available.map((player) => <option value={player.user_id} key={player.user_id}>{player.name || player.account_name || player.user_id}</option>)}</select></label> : <label className="field"><span>User ID</span><input value={manual} onChange={(event) => setManual(event.target.value)} /></label>}{error && <p className="form-error" role="alert">{error}</p>}<button className="primary-button" type="button" onClick={submit}>Create override</button></section></div>;
}

function clonePolicy(policy: Policies): Policies {
  return structuredClone(policy);
}
