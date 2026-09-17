import { memo, useMemo, useState } from 'react';
import { ChevronRight, Search, Users } from 'lucide-react';
import type { LivePositionPlayer, Player } from '../api';
import { playerColor } from '../map/worldMapMarkers';

type Props = { players: Player[]; live: LivePositionPlayer[]; selectedID: string; mode: 'live' | 'history'; onSelect: (id: string) => void; onClose: () => void; children?: React.ReactNode };
export const WorkspaceRoster = memo(function WorkspaceRoster({ players, live, selectedID, mode, onSelect, onClose, children }: Props) {
  const [query, setQuery] = useState('');
  const [onlineOnly, setOnlineOnly] = useState(false);
  const entries = useMemo(() => {
    const byID = new Map(players.map(p => [p.user_id, { id: p.user_id, name: p.name || p.account_name || p.user_id, online: p.online, level: undefined as number | undefined, positioned: false }]));
    live.forEach(p => byID.set(p.user_id, { id: p.user_id, name: p.name || p.account_name || p.user_id, online: true, level: p.level, positioned: true }));
    return Array.from(byID.values()).sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
  }, [players, live]);
  const shown = entries.filter(p => (!onlineOnly || p.online) && `${p.name} ${p.id}`.toLowerCase().includes(query.trim().toLowerCase()));
  return <aside className="world-roster world-glass" aria-label="玩家列表">
    <header className="world-roster-heading"><div><Users size={16} /><h2>玩家</h2><span>{entries.length}</span></div><button type="button" onClick={onClose} aria-label="收起玩家列表"><ChevronRight size={18} /></button></header>
    <label className="world-search"><Search size={16} /><input type="search" aria-label="搜索地图玩家" placeholder="寻找一位探险者…" value={query} onChange={e => setQuery(e.target.value)} /></label>
    <div className="world-roster-filter"><span>{mode === 'history' ? '名单状态为当前状态' : '世界中的探险者'}</span><button type="button" aria-pressed={onlineOnly} onClick={() => setOnlineOnly(v => !v)}>仅在线</button></div>
    <ul className="world-roster-list">
      {shown.map(p => <li key={p.id}><button type="button" aria-label={`选择玩家 ${p.name}`} aria-pressed={selectedID === p.id} onClick={() => onSelect(p.id)}>
        <span className="world-avatar" style={{ color: playerColor(p.id) }}>{Array.from(p.name)[0]}<i className={p.online ? 'is-online' : ''} /></span>
        <span className="world-player-name"><strong>{p.name}</strong><small>{p.online ? p.positioned ? '当前在线' : '在线 · 暂无位置' : '当前离线'}</small></span>
        {mode === 'live' && p.level ? <span className="world-player-level">Lv.{p.level}</span> : <ChevronRight size={14} />}
      </button></li>)}
    </ul>
    {!shown.length ? <p className="world-empty-roster">{entries.length ? '没有匹配的玩家' : '等待第一位探险者出现'}</p> : null}
    {children}
  </aside>;
});
