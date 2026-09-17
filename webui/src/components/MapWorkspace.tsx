import { useCallback, useEffect, useMemo, useState } from 'react';
import { ArrowUpRight, ChevronDown, Compass, Crosshair, History, Layers, LocateFixed, Radio, RefreshCw, Route, Users } from 'lucide-react';
import { getGuildBases, type Player, type ProgressChange, type WorldPOI } from '../api';
import { WorldMap } from '../map/WorldMap';
import type { MapDisplayPoint } from '../map/worldMapMarkers';
import { BREAK_LABELS, playbackFrame, prepareTrajectory } from '../map/workspacePlayback';
import { useLivePositions, useHistoryWindow } from '../map/useWorkspaceData';
import { usePlaybackClock } from '../map/usePlaybackClock';
import { WorkspaceRoster } from './WorkspaceRoster';
import { WorkspacePlaybackBar, workspaceTime } from './WorkspacePlaybackBar';
import { WorkspaceProgress } from './WorkspaceProgress';
import { usePlayerProgress } from '../map/usePlayerProgress';
import { progressBounds } from '../map/workspaceProgress';
import { WorkspaceJourney } from './WorkspaceJourney';
import { useJourneyTimeline } from '../map/useJourneyTimeline';
import { useJourneyCursor } from '../map/useJourneyCursor';
import { summarizeJourney } from '../map/journeySummary';
import type { JourneyHeatCell } from '../map/journeyTypes';
import { eventLabel } from './timelineShared';

type Props = { players: Player[]; refreshKey: number; active?: boolean; initialSelectedID?: string; onSelectPlayer?: (id: string) => void; onOpenPlayer?: (id: string) => void };
const EMPTY_LIVE: NonNullable<ReturnType<typeof useLivePositions>['data']>['players'] = [];
const EMPTY_EVENTS: NonNullable<ReturnType<typeof useHistoryWindow>['data']>['events'] = [];
const EMPTY_SAMPLES: ReturnType<typeof prepareTrajectory> = [];
const EMPTY_HEAT: JourneyHeatCell[] = [];

export function MapWorkspace({ players, refreshKey, active = true, initialSelectedID = '', onSelectPlayer, onOpenPlayer }: Props) {
  const [selectedID, setSelectedID] = useState(initialSelectedID);
  const [mode, setMode] = useState<'live' | 'history'>('live');
  const [rosterOpen, setRosterOpen] = useState(() => window.innerWidth >= 768);
  const [follow, setFollow] = useState(false);
  const [focusRequest, setFocusRequest] = useState(0);
  const [showLandmarks, setShowLandmarks] = useState(false);
  const [showBases, setShowBases] = useState(false);
  const [showTrail, setShowTrail] = useState(true);
  const [showHeat, setShowHeat] = useState(false);
  const [detailTab, setDetailTab] = useState<'progress' | 'journey'>('progress');
  const [focusArea, setFocusArea] = useState<{ x: number; y: number; request: number }>();
  const [journeyRevision, setJourneyRevision] = useState(0);
  const [bases, setBases] = useState<WorldPOI[]>([]);
  const [basesError, setBasesError] = useState(false);
  const [windowRange, setWindowRange] = useState(() => ({ start: Date.now() - 86400000, end: Date.now(), label: '24h' }));
  const [refresh, setRefresh] = useState(0);
  const [historyRevision, setHistoryRevision] = useState(0);
  const [progressRevision, setProgressRevision] = useState(0);
  const [pendingProgressSeek, setPendingProgressSeek] = useState<{ userID: string; time: number } | null>(null);
  const progress = usePlayerProgress(active, selectedID, mode, windowRange.start, windowRange.end, progressRevision + historyRevision);
  const progressRange = useMemo(() => progressBounds(progress.data), [progress.data]);
  const live = useLivePositions(refreshKey + refresh, active);
  const history = useHistoryWindow(mode === 'history' && active, selectedID, windowRange.start, windowRange.end, historyRevision);
  const journeyLive = useJourneyTimeline(active && mode === 'live' && (detailTab === 'journey' || showHeat), selectedID, windowRange.end - windowRange.start, journeyRevision + refreshKey);
  const livePlayers = live.data?.players ?? EMPTY_LIVE;
  const samples = useMemo(() => prepareTrajectory(history.data?.trajectories ?? []), [history.data]);
  const events = useMemo(() => [...(history.data?.events ?? EMPTY_EVENTS)].filter(e => Number.isFinite(Date.parse(e.occurred_at))).sort((a, b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at)), [history.data]);
  const start = Math.max(windowRange.start, Math.min(progressRange.start, samples[0]?.time ?? Infinity, events.length ? Date.parse(events[0].occurred_at) : Infinity));
  const end = Math.min(windowRange.end, Date.now(), Math.max(progressRange.end, samples.at(-1)?.time ?? -Infinity, events.length ? Date.parse(events.at(-1)!.occurred_at) : -Infinity));
  const clock = usePlaybackClock(Number.isFinite(start) ? start : windowRange.start, Number.isFinite(end) ? end : windowRange.end, `${selectedID}:${windowRange.start}:${windowRange.end}`);
  const frame = useMemo(() => playbackFrame(samples, clock.time), [samples, clock.time]);
  const journeyCursor = useJourneyCursor(clock.time, active && mode === 'history' && clock.playing && (detailTab === 'journey' || showHeat), `${selectedID}:${windowRange.start}:${windowRange.end}`);
  const journeyStart = mode === 'history' ? windowRange.start : journeyLive.start ?? windowRange.start;
  const journeyEnd = Math.min(Date.now(), mode === 'history' ? journeyCursor : Math.floor(Date.now() / 1000) * 1000);
  const journeyTimeline = mode === 'history' ? history.data : journeyLive.data;
  const journeyError = (mode === 'history' ? history.error : journeyLive.error) || progress.error;
  const journeyNeeded = Boolean(selectedID) && (detailTab === 'journey' || showHeat);
  const journey = useMemo(() => {
    if (!journeyNeeded) return undefined;
    const result = summarizeJourney({ userID: selectedID, start: journeyStart, end: Math.max(journeyStart, journeyEnd), timeline: journeyTimeline, progress: progress.data });
    if (journeyError) { result.inferences = []; result.warnings = [...new Set([...result.warnings, 'data_unavailable'])]; }
    return result;
  }, [journeyNeeded, selectedID, journeyStart, journeyEnd, journeyTimeline, progress.data, journeyError]);
  useEffect(() => {
    if (!pendingProgressSeek || mode !== 'history') return;
    if (pendingProgressSeek.userID !== selectedID) { setPendingProgressSeek(null); return; }
    if (history.loading || progress.loading || (!history.data && !history.error) || (!progress.data && !progress.error)) return;
    clock.seek(pendingProgressSeek.time);
    setPendingProgressSeek(null);
  }, [pendingProgressSeek, mode, selectedID, history.loading, history.data, history.error, progress.loading, progress.data, progress.error, clock.seek]);
  const selected = players.find(p => p.user_id === selectedID);
  const selectedLive = livePlayers.find(p => p.user_id === selectedID);
  const selectedName = selected?.name || selectedLive?.name || selected?.account_name || selectedID;

  useEffect(() => { setSelectedID(initialSelectedID); }, [initialSelectedID]);
  useEffect(() => { setFocusArea(undefined); }, [selectedID, mode]);
  useEffect(() => { if (mode === 'live' || !active) clock.setPlaying(false); }, [mode, active, clock.setPlaying]);
  useEffect(() => {
    if (!showBases || !active) return;
    const controller = new AbortController();
    setBasesError(false);
    void getGuildBases(controller.signal).then(data => { if (!controller.signal.aborted) setBases(data.pois ?? []); })
      .catch(() => { if (!controller.signal.aborted) setBasesError(true); });
    return () => controller.abort();
  }, [showBases, refreshKey, refresh, active]);

  const selectPlayer = useCallback((id: string) => { setSelectedID(id); setFollow(false); setFocusRequest(v => v + 1); if (window.innerWidth < 768) setRosterOpen(false); onSelectPlayer?.(id); }, [onSelectPlayer]);
  const stopFollow = useCallback(() => setFollow(false), []);
  const closeRoster = useCallback(() => setRosterOpen(false), []);
  const points = useMemo<MapDisplayPoint[]>(() => mode === 'live'
    ? livePlayers.filter(p => Number.isFinite(p.x) && Number.isFinite(p.y)).map(p => ({ ...p, name: p.name || p.account_name || p.user_id, observedAt: live.data?.as_of, continuity: 'live', state: '当前位置' }))
    : frame ? [{ user_id: selectedID, name: selectedName, x: frame.x, y: frame.y, level: frame.sample.level, observedAt: frame.sample.observed_at, continuity: `history:${frame.sample.segment_id}:${frame.sample.runtime_epoch}`, state: frame.status === 'gap' ? '观测缺口' : frame.status === 'last-known' ? '最后观测' : frame.interpolated ? '回放位置 · 插值' : '回放位置' }] : [],
  [mode, livePlayers, live.data?.as_of, frame, selectedID, selectedName]);

  const truncated = Boolean(history.data && ((history.data.trajectory_total ?? 0) > samples.length || (history.data.event_total ?? 0) > events.length || history.data.trajectories.length >= 500 || events.length >= 500));
  const historical = mode === 'history';
  const selectedPoint = points.find(p => p.user_id === selectedID);
  const disabled = history.loading || !Number.isFinite(start) || !Number.isFinite(end) || end <= start;
  function enterHistory() {
    if (!history.data && windowRange.label.endsWith('h')) changeRange(Number(windowRange.label.slice(0, -1)));
    setMode('history'); setFollow(false);
  }
  function seekProgress(change: ProgressChange) {
    seekJourney(Date.parse(change.interval_end));
  }
  const seekJourney = useCallback((time: number) => {
    if (!Number.isFinite(time) || time > Date.now()) return;
    clock.setPlaying(false);
    if (mode === 'history') { clock.seek(time); return; }
    const now = Date.now();
    setWindowRange({ start: now - (windowRange.end - windowRange.start), end: now, label: windowRange.label });
    setPendingProgressSeek({ userID: selectedID, time });
    setMode('history'); setFollow(false);
  }, [clock.setPlaying, clock.seek, mode, windowRange, selectedID]);
  const focusDwell = useCallback((cell: JourneyHeatCell) => {
    setFollow(false); setShowHeat(true);
    setFocusArea(previous => ({ x: cell.x, y: cell.y, request: (previous?.request ?? 0) + 1 }));
  }, []);
  const changeRange = useCallback((hours: number) => { const end = Date.now(); setWindowRange({ start: end - hours * 3600000, end, label: `${hours}h` }); }, []);
  const reloadHistory = useCallback(() => {
    clock.setPlaying(false);
    if (windowRange.label.endsWith('h')) changeRange(Number(windowRange.label.slice(0, -1)));
    setHistoryRevision(v => v + 1);
  }, [clock.setPlaying, windowRange.label, changeRange]);
  const retryJourney = useCallback(() => {
    setJourneyRevision(v => v + 1); setProgressRevision(v => v + 1);
    if (historical) reloadHistory();
  }, [historical, reloadHistory]);
  const progressCard = selectedID ? <>
    <div className="world-detail-tabs" aria-label="玩家详情"><button type="button" aria-pressed={detailTab === 'progress'} onClick={() => setDetailTab('progress')}>进度变化</button><button type="button" aria-pressed={detailTab === 'journey'} onClick={() => setDetailTab('journey')}>游玩小结</button></div>
    {detailTab === 'progress' ? <WorkspaceProgress key={selectedID} name={selectedName} data={progress.data} mode={mode} cursor={mode === 'history' ? clock.time : Date.now()} loading={progress.loading} error={progress.error} onChange={seekProgress} onRetry={() => setProgressRevision(v => v + 1)} /> : <>
      {!historical ? <label className="world-journey-window">观察窗口<select aria-label="小结观察范围" value={windowRange.label.endsWith('h') ? windowRange.label : '24h'} onChange={e => changeRange(Number(e.target.value.slice(0, -1)))}><option value="1h">最近 1 小时</option><option value="6h">最近 6 小时</option><option value="24h">最近 24 小时</option><option value="168h">最近 7 天</option></select></label> : null}
      <WorkspaceJourney key={selectedID} name={selectedName} summary={journey!} loading={(historical ? history.loading : journeyLive.loading) || progress.loading} error={journeyError}
        onSeek={seekJourney} onFocus={focusDwell} onRetry={retryJourney} />
    </>}
  </> : null;
  return <section className={`world-workspace ${historical ? 'is-history' : 'is-live'} ${rosterOpen ? 'roster-open' : ''}`} aria-label="地图工作台">
    <WorldMap mode={mode} points={points} selectedID={selectedID} samples={historical ? samples : EMPTY_SAMPLES} cursorTime={clock.time}
      showTrail={showTrail} showLandmarks={showLandmarks} showBases={showBases} bases={bases} follow={follow} focusRequest={focusRequest}
      stale={mode === 'live' && live.stale} onSelect={selectPlayer} onInteraction={stopFollow}
      heat={showHeat ? journey?.heat ?? EMPTY_HEAT : EMPTY_HEAT} focusArea={focusArea} onFocusArea={focusDwell} />
    <div className="world-vignette" aria-hidden="true" />
    <div className="world-heading"><span className="world-eyebrow"><Compass size={14} /> PALWORLD ATLAS</span><h2>世界正在发生<span>。</span></h2><p>{historical ? '沿着足迹，回到那一刻。' : '每位探险者，都有自己的旅程。'}</p></div>
    <div className="world-top-controls">
      <div className="world-mode-switch world-glass" aria-label="地图模式"><button type="button" aria-pressed={!historical} onClick={() => setMode('live')}><Radio size={15} />实时</button><button type="button" aria-label="切换历史模式" aria-pressed={historical} onClick={enterHistory}><History size={15} />回放</button></div>
      <details className="world-layers world-glass"><summary aria-label="地图图层"><Layers size={17} /><span>图层</span></summary><div>
        <label><input type="checkbox" checked={showLandmarks} onChange={e => setShowLandmarks(e.target.checked)} />传送点与高塔</label>
        <label><input type="checkbox" checked={showBases} onChange={e => setShowBases(e.target.checked)} />公会据点</label>
        <label><input type="checkbox" checked={showTrail} onChange={e => setShowTrail(e.target.checked)} />回放足迹</label>
        <label><input type="checkbox" checked={showHeat} disabled={!selectedID} onChange={e => setShowHeat(e.target.checked)} />停留热度</label>
        {showHeat ? <p>按有效停留时长加权 · 近似网格区域</p> : null}
        {basesError ? <p role="status">据点暂时不可用</p> : null}
      </div></details>
    </div>
    <div className={`world-data-state ${live.stale ? 'is-stale' : ''}`}><i /><span>{historical ? '历史观察' : live.error ? '连接中断 · 保留最后位置' : !live.data ? '正在连接世界…' : live.stale ? '位置已陈旧' : `${live.data.online_count} 人在线`}</span>{!historical && live.data ? <small>{Number.isFinite(live.ageMs) ? `${Math.floor(live.ageMs / 1000)} 秒前观测` : '观测时间未知'}</small> : null}<button type="button" aria-label={historical ? '重新加载历史' : '刷新地图位置'} onClick={historical ? reloadHistory : () => setRefresh(v => v + 1)}><RefreshCw size={14} /></button></div>
    {!rosterOpen ? <button className="world-roster-open world-glass" type="button" aria-label="展开玩家列表" onClick={() => setRosterOpen(true)}><Users size={17} /><span>玩家</span></button> : null}
    {rosterOpen ? <WorkspaceRoster players={players} live={livePlayers} selectedID={selectedID} mode={mode} onSelect={selectPlayer} onClose={closeRoster}>
      {selectedID ? <div className="world-selected-detail"><div className="world-detail-label"><span>{historical ? '正在回看' : '选中玩家'}</span><strong>{selectedName}</strong></div>
        <div className="world-selected-actions"><button type="button" disabled={!selectedPoint} onClick={() => setFocusRequest(v => v + 1)}><Crosshair size={15} />定位</button><button type="button" aria-label={follow ? '停止跟随' : '跟随玩家'} aria-pressed={follow} disabled={!selectedPoint} onClick={() => { setFollow(v => !v); if (!follow) setFocusRequest(v => v + 1); }}><LocateFixed size={15} />{follow ? '跟随中' : '跟随'}</button></div>
        {progressCard}
        {!historical ? <button className="world-detail-link" type="button" onClick={enterHistory}>回看这位玩家<ArrowUpRight size={15} /></button> : <>
          <div className="world-history-numbers"><div><strong>{samples.length}</strong><span>位置观测</span></div><div><strong>{events.length}</strong><span>事件记录</span></div></div>
          <details className="world-events"><summary>事件记录<ChevronDown size={14} /></summary><ol>{events.map(event => <li key={event.id}><button type="button" onClick={() => clock.seek(Date.parse(event.occurred_at))}><time>{workspaceTime(Date.parse(event.occurred_at))}</time><strong>{eventLabel(event.event_type)}</strong><small>{event.confidence === 'snapshot_derived' ? '存档推导' : '已观测'}</small></button></li>)}</ol>{!events.length ? <p>当前区间没有事件记录</p> : null}</details>
        </>}
        {onOpenPlayer ? <button className="world-detail-link" type="button" onClick={() => onOpenPlayer(selectedID)}>完整证据与行为分析<ArrowUpRight size={15} /></button> : null}
      </div> : <p className="world-roster-tip">选择一位玩家，定位或回看旅程。</p>}
    </WorkspaceRoster> : null}
    {!rosterOpen && progressCard ? <div className="world-progress-panel world-glass">{progressCard}</div> : null}
    {historical ? <div className="world-history-tools world-glass"><Route size={15} /><span>观察窗口</span><select aria-label="历史时间范围" value={windowRange.label} onChange={e => changeRange(Number(e.target.value.replace('h', '')))}><option value="1h">最近 1 小时</option><option value="6h">最近 6 小时</option><option value="24h">最近 24 小时</option><option value="168h">最近 7 天</option>{windowRange.label === 'day' ? <option value="day">指定日期</option> : null}</select><input type="date" aria-label="回看指定日期" onChange={e => { if (!e.target.value) return; const begin = new Date(`${e.target.value}T00:00:00`); const end = new Date(begin); end.setDate(end.getDate() + 1); if (Number.isFinite(begin.getTime())) setWindowRange({ start: begin.getTime(), end: end.getTime(), label: 'day' }); }} /></div> : null}
    <div className="world-messages" aria-live="polite">
      {historical && !selectedID ? <p>选择玩家，开始回看旅程</p> : null}
      {historical && history.loading ? <p>正在寻找过去的足迹…</p> : null}
      {historical && history.error ? <p role="alert">{history.error}<button type="button" onClick={reloadHistory}>重试</button></p> : null}
      {historical && history.data && !samples.length ? <p>这个时间段没有位置记录</p> : null}
      {historical && truncated ? <p>当前仅加载部分记录 · 位置 {samples.length}/{history.data?.trajectory_total ?? '更多'} · 事件 {events.length}/{history.data?.event_total ?? '更多'}</p> : null}
      {historical && frame?.status === 'gap' ? <p>{frame.reason ? BREAK_LABELS[frame.reason] : '观测缺口'} · 保留上次位置{frame.nextAt ? <button type="button" onClick={() => clock.seek(frame.nextAt!)}>跳到下一次观测</button> : null}</p> : null}
      {historical && frame?.status === 'last-known' ? <p>此刻没有新的位置观测</p> : null}
      {!historical && live.data && !livePlayers.length ? <p>{live.data.online_count ? '在线玩家暂时没有位置观测' : '此刻世界很安静，等待玩家上线。'}</p> : null}
      {!historical && selectedID && !selectedLive ? <p>这位玩家暂无实时位置，可以回看历史足迹。</p> : null}
    </div>
    <WorkspacePlaybackBar mode={mode} start={Number.isFinite(start) ? start : windowRange.start} end={Number.isFinite(end) ? end : windowRange.end}
      time={clock.time} playing={clock.playing} speed={clock.speed} disabled={disabled} events={events} onSeek={clock.seek} onPlaying={clock.setPlaying} onSpeed={clock.setSpeed} onLive={() => setMode('live')} onHistory={enterHistory} />
  </section>;
}
