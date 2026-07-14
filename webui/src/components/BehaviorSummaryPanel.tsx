import { Activity } from 'lucide-react';
import { GAP_SHARE_WARN, type BehaviorSummary, type POIDwell } from '../behavior/behaviorTypes';
import {
  BEHAVIOR_CLASS_LABELS,
  formatBehaviorDistance,
  formatBehaviorShare,
  formatBehaviorSpeed,
  formatDensityPerHour,
  formatDominantLabel,
  formatPOIKind,
  formatTeleportLine,
  formatTeleportReason,
} from '../behavior/behaviorFormat';
import { formatDuration } from '../utils';

export type BehaviorSummaryPanelProps = {
  summary: BehaviorSummary | null;
  loading: boolean;
  selected: boolean;
  /** Compact floating panel over the map. */
  variant?: 'inline' | 'overlay';
  /** Index into summary.teleportSuspects currently shown on the map; null = none. */
  highlightedTeleportIndex?: number | null;
  onHighlightTeleport?: (index: number | null) => void;
  /** POI id focused on the map (anchor or dwell). */
  highlightedPOIId?: string | null;
  onFocusPOI?: (poiId: string | null) => void;
};

function hasCoords(poi: Pick<POIDwell, 'x' | 'y'>): boolean {
  return Number.isFinite(poi.x) && Number.isFinite(poi.y);
}

export function BehaviorSummaryPanel({
  summary,
  loading,
  selected,
  variant = 'inline',
  highlightedTeleportIndex = null,
  onHighlightTeleport,
  highlightedPOIId = null,
  onFocusPOI,
}: BehaviorSummaryPanelProps) {
  if (!selected) return null;

  function togglePOI(poi: POIDwell) {
    if (!hasCoords(poi)) return;
    onFocusPOI?.(highlightedPOIId === poi.poiId ? null : poi.poiId);
  }

  return (
    <section
      className={`behavior-summary${variant === 'overlay' ? ' behavior-summary--overlay' : ''}`}
      aria-label="行为摘要"
    >
      <header className="behavior-summary-header">
        <div>
          <p className="eyebrow">轨迹分析</p>
          <h3>行为摘要</h3>
          <p className="behavior-summary-note">基于已加载的位置样本 · 与政策在线时长不同</p>
        </div>
        <span className="behavior-summary-badge">
          <Activity size={15} />
          {summary && summary.sampleCount > 0 ? formatDominantLabel(summary.dominantClass) : '—'}
        </span>
      </header>

      {loading ? <p className="behavior-summary-empty">正在分析轨迹…</p> : null}

      {!loading && (!summary || summary.sampleCount === 0) ? (
        <p className="behavior-summary-empty">当前范围无位置样本，无法估计跑图/挂机行为。</p>
      ) : null}

      {!loading && summary && summary.sampleCount > 0 ? (
        <>
          <div className="behavior-mix" role="img" aria-label="行为占比">
            {(['traveling', 'local', 'stationary'] as const).map((key) => (
              <div
                className={`behavior-mix-seg behavior-mix-seg--${key}`}
                key={key}
                style={{ flexGrow: Math.max(summary.classShare[key], 0.02) }}
              >
                <span>
                  {BEHAVIOR_CLASS_LABELS[key]} {formatBehaviorShare(summary.classShare[key])}
                </span>
              </div>
            ))}
          </div>
          <dl className="behavior-metrics">
            <div>
              <dt>观测活跃</dt>
              <dd>{formatDuration(summary.observedActiveMs)}</dd>
            </div>
            <div>
              <dt>活动半径</dt>
              <dd>{formatBehaviorDistance(summary.radius)}</dd>
            </div>
            <div>
              <dt>路径长度</dt>
              <dd>{formatBehaviorDistance(summary.pathLength)}</dd>
            </div>
            <div>
              <dt>均速</dt>
              <dd>{formatBehaviorSpeed(summary.meanSpeed)}</dd>
            </div>
            <div>
              <dt>峰值速度</dt>
              <dd>{formatBehaviorSpeed(summary.peakSpeed)}</dd>
            </div>
            <div>
              <dt>采样密度</dt>
              <dd>{formatDensityPerHour(summary.sampleDensityPerHour)}</dd>
            </div>
            <div>
              <dt>位置点数</dt>
              <dd>{summary.sampleCount}</dd>
            </div>
            <div>
              <dt>轨迹段</dt>
              <dd>{summary.segmentCount}</dd>
            </div>
          </dl>

          {summary.activityAnchor ? (
            <div className="behavior-poi-block">
              <h4 className="behavior-poi-heading">活动锚点</h4>
              <p className="behavior-poi-hint">点击可在地图上定位</p>
              {hasCoords(summary.activityAnchor) ? (
                <button
                  type="button"
                  className={`behavior-poi-item${highlightedPOIId === summary.activityAnchor.poiId ? ' is-active' : ''}`}
                  aria-pressed={highlightedPOIId === summary.activityAnchor.poiId}
                  onClick={() => togglePOI(summary.activityAnchor!)}
                >
                  <span className="behavior-poi-name">{summary.activityAnchor.nameZh}</span>
                  <span
                    className={`behavior-poi-kind behavior-poi-kind--${summary.activityAnchor.kind}`}
                  >
                    {formatPOIKind(summary.activityAnchor.kind)}
                  </span>
                  <span className="behavior-poi-duration">
                    {formatDuration(summary.activityAnchor.dwellMs)}
                  </span>
                </button>
              ) : (
                <div className="behavior-poi-anchor">
                  <span className="behavior-poi-name">{summary.activityAnchor.nameZh}</span>
                  <span
                    className={`behavior-poi-kind behavior-poi-kind--${summary.activityAnchor.kind}`}
                  >
                    {formatPOIKind(summary.activityAnchor.kind)}
                  </span>
                  <span className="behavior-poi-duration">
                    {formatDuration(summary.activityAnchor.dwellMs)}
                  </span>
                </div>
              )}
            </div>
          ) : null}

          {summary.poiDwells.length > 0 ? (
            <div className="behavior-poi-block">
              <h4 className="behavior-poi-heading">驻留</h4>
              <p className="behavior-poi-hint">点击传送点/据点可定位到地图</p>
              <ol className="behavior-poi-list behavior-poi-list--clickable">
                {summary.poiDwells.map((dwell) => {
                  const focusable = hasCoords(dwell);
                  const active = highlightedPOIId === dwell.poiId;
                  if (!focusable) {
                    return (
                      <li key={dwell.poiId}>
                        <span className="behavior-poi-name">{dwell.nameZh}</span>
                        <span className={`behavior-poi-kind behavior-poi-kind--${dwell.kind}`}>
                          {formatPOIKind(dwell.kind)}
                        </span>
                        <span className="behavior-poi-duration">{formatDuration(dwell.dwellMs)}</span>
                      </li>
                    );
                  }
                  return (
                    <li key={dwell.poiId}>
                      <button
                        type="button"
                        className={`behavior-poi-item${active ? ' is-active' : ''}`}
                        aria-pressed={active}
                        onClick={() => togglePOI(dwell)}
                      >
                        <span className="behavior-poi-name">{dwell.nameZh}</span>
                        <span className={`behavior-poi-kind behavior-poi-kind--${dwell.kind}`}>
                          {formatPOIKind(dwell.kind)}
                        </span>
                        <span className="behavior-poi-duration">{formatDuration(dwell.dwellMs)}</span>
                      </button>
                    </li>
                  );
                })}
              </ol>
            </div>
          ) : null}

          {summary.guildPresence ? (
            <div className="behavior-poi-block">
              <h4 className="behavior-poi-heading">公会据点</h4>
              <p className="behavior-poi-guild">
                公会据点停留 · {formatDuration(summary.guildPresence.dwellMs)}
                {summary.guildPresence.baseCount > 0
                  ? ` · ${summary.guildPresence.baseCount} 处`
                  : null}
                {summary.guildPresence.guildName
                  ? ` · ${summary.guildPresence.guildName}`
                  : null}
              </p>
            </div>
          ) : null}

          {summary.teleportSuspects.length > 0 ? (
            <div className="behavior-poi-block">
              <h4 className="behavior-poi-heading">疑似传送</h4>
              <p className="behavior-poi-hint">点击一条显示连线并定位到地图</p>
              <ol className="behavior-poi-list behavior-poi-list--clickable">
                {summary.teleportSuspects.map((t, i) => {
                  const active = highlightedTeleportIndex === i;
                  return (
                    <li key={`${t.at}-${i}`}>
                      <button
                        type="button"
                        className={`behavior-teleport-item${active ? ' is-active' : ''}`}
                        aria-pressed={active}
                        onClick={() => onHighlightTeleport?.(active ? null : i)}
                      >
                        <span className="behavior-poi-name">{formatTeleportLine(t)}</span>
                        <span className="behavior-teleport-reason">
                          {formatTeleportReason(t.reason)}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ol>
            </div>
          ) : null}

          {summary.poiDwells.length === 0 && summary.teleportSuspects.length === 0 ? (
            <p className="behavior-summary-empty">
              未匹配到传送点、首领塔或公会据点附近的驻留
            </p>
          ) : null}

          {summary.gapShareOfWindow > GAP_SHARE_WARN ? (
            <p className="behavior-summary-gap" role="status">
              存在观测断档，活跃时长未覆盖全部日历时间。
            </p>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
