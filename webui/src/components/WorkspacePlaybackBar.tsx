import { Pause, Play, Radio, SkipBack, SkipForward } from 'lucide-react';
import type { TimelineEvent } from '../api';
import { eventLabel } from './timelineShared';
import { WORKSPACE_SPEEDS, type WorkspaceSpeed } from '../map/usePlaybackClock';

const timeFormat = new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
export const workspaceTime = (ms: number) => Number.isFinite(ms) ? timeFormat.format(ms) : '—';

type Props = {
  mode: 'live' | 'history'; start: number; end: number; time: number; playing: boolean; speed: WorkspaceSpeed;
  disabled: boolean; events: TimelineEvent[]; onSeek: (time: number) => void; onPlaying: (playing: boolean) => void;
  onSpeed: (speed: WorkspaceSpeed) => void; onLive: () => void; onHistory: () => void;
};
export function WorkspacePlaybackBar(p: Props) {
  const progress = p.end > p.start ? Math.max(0, Math.min(100, 100 * (p.time - p.start) / (p.end - p.start))) : 0;
  return <footer className="world-playback world-glass" aria-label="地图时间控制">
    <div className="world-playback-main">
      {p.mode === 'live' ? <>
        <span className="world-live-indicator"><i />正在观察世界</span>
        <span className="world-playback-hint">每一次停留，都有迹可循。</span>
        <button className="world-history-button" type="button" onClick={p.onHistory}>历史回放<SkipBack size={16} /></button>
      </> : <>
        <div className="world-transport">
          <button type="button" aria-label="回到起点" disabled={p.disabled} onClick={() => p.onSeek(p.start)}><SkipBack size={17} /></button>
          <button type="button" className="world-play-button" aria-label={p.playing ? '暂停' : '播放'} disabled={p.disabled} onClick={() => p.onPlaying(!p.playing)}>{p.playing ? <Pause size={19} /> : <Play size={19} />}</button>
          <button type="button" aria-label="前进一分钟" disabled={p.disabled} onClick={() => p.onSeek(p.time + 60000)}><SkipForward size={17} /></button>
        </div>
        <div className="world-playback-time"><strong>{workspaceTime(p.time)}</strong><span>1× 每秒回看 1 分钟</span></div>
        <select aria-label="回放速度" value={p.speed} onChange={e => p.onSpeed(Number(e.target.value) as WorkspaceSpeed)}>{WORKSPACE_SPEEDS.map(s => <option key={s} value={s}>{s}×</option>)}</select>
        <button className="world-return-live" type="button" aria-label="回到实时" onClick={p.onLive}><Radio size={15} /><span>回到实时</span></button>
      </>}
    </div>
    {p.mode === 'history' ? <div className="world-scrubber">
      <div className="world-event-ticks" aria-hidden="true">{p.events.filter(e => Date.parse(e.occurred_at) >= p.start && Date.parse(e.occurred_at) <= p.end).map(e => <i key={e.id} title={eventLabel(e.event_type)} style={{ left: `${100 * (Date.parse(e.occurred_at) - p.start) / Math.max(1, p.end - p.start)}%` }} />)}</div>
      <input type="range" aria-label="回放时间" aria-valuetext={workspaceTime(p.time)} min={p.start} max={Math.max(p.start + 1, p.end)} step={1} value={p.time} disabled={p.disabled} onChange={e => p.onSeek(Number(e.target.value))}
        onKeyDown={e => {
          const direction = ['ArrowRight', 'ArrowUp'].includes(e.key) ? 1 : ['ArrowLeft', 'ArrowDown'].includes(e.key) ? -1 : 0;
          if (direction) { e.preventDefault(); p.onSeek(p.time + direction * (e.shiftKey ? 60000 : 1000)); }
          else if (e.key === 'Home' || e.key === 'End') { e.preventDefault(); p.onSeek(e.key === 'Home' ? p.start : p.end); }
        }} style={{ '--progress': `${progress}%` } as React.CSSProperties} />
      <div className="world-scrubber-labels"><time>{workspaceTime(p.start)}</time><span>回放范围</span><time>{workspaceTime(p.end)}</time></div>
    </div> : null}
  </footer>;
}
