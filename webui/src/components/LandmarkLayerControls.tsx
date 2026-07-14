import { useMemo } from 'react';
import type { WorldPOI } from '../api';
import { guildOptionsFromPOIs } from '../map/guildLandmarks';

export type LandmarkLayerControlsProps = {
  showStatic: boolean;
  onShowStaticChange: (value: boolean) => void;
  showGuildBases: boolean;
  onShowGuildBasesChange: (value: boolean) => void;
  guildBases: WorldPOI[];
  /** null = all guilds enabled; empty set = none. */
  enabledGuildIDs: Set<string> | null;
  onEnabledGuildIDsChange: (next: Set<string> | null) => void;
  guildFilterOpen: boolean;
  onGuildFilterOpenChange: (open: boolean) => void;
};

export function LandmarkLayerControls({
  showStatic,
  onShowStaticChange,
  showGuildBases,
  onShowGuildBasesChange,
  guildBases,
  enabledGuildIDs,
  onEnabledGuildIDsChange,
  guildFilterOpen,
  onGuildFilterOpenChange,
}: LandmarkLayerControlsProps) {
  const options = useMemo(() => guildOptionsFromPOIs(guildBases), [guildBases]);
  const enabledCount = enabledGuildIDs == null ? options.length : enabledGuildIDs.size;

  function toggleGuild(id: string) {
    const allIDs = options.map((o) => o.id);
    const current = enabledGuildIDs == null ? new Set(allIDs) : new Set(enabledGuildIDs);
    if (current.has(id)) current.delete(id);
    else current.add(id);
    if (current.size === allIDs.length) {
      onEnabledGuildIDsChange(null);
      return;
    }
    onEnabledGuildIDsChange(current);
  }

  function selectAll() {
    onEnabledGuildIDsChange(null);
  }

  function selectNone() {
    onEnabledGuildIDsChange(new Set());
  }

  return (
    <div className="landmark-layer-controls">
      <div className="timeline-layer-toggles" role="group" aria-label="地标图层">
        <button
          type="button"
          className="timeline-layer-toggle"
          aria-pressed={showStatic}
          onClick={() => onShowStaticChange(!showStatic)}
          title="传送点与首领塔"
        >
          地标
        </button>
        <button
          type="button"
          className="timeline-layer-toggle timeline-layer-toggle--guild"
          aria-pressed={showGuildBases}
          onClick={() => {
            const next = !showGuildBases;
            onShowGuildBasesChange(next);
            if (next && options.length > 0) onGuildFilterOpenChange(true);
          }}
          title="公会据点（图标不同）"
        >
          公会据点
          {showGuildBases && options.length > 0 ? (
            <span className="landmark-guild-count">{enabledCount}/{options.length}</span>
          ) : null}
        </button>
        {showGuildBases && options.length > 0 ? (
          <button
            type="button"
            className="timeline-layer-toggle"
            aria-expanded={guildFilterOpen}
            onClick={() => onGuildFilterOpenChange(!guildFilterOpen)}
          >
            筛选
          </button>
        ) : null}
      </div>
      {showGuildBases && guildFilterOpen && options.length > 0 ? (
        <div className="landmark-guild-filter" role="group" aria-label="公会据点筛选">
          <div className="landmark-guild-filter-actions">
            <button type="button" onClick={selectAll}>全选</button>
            <button type="button" onClick={selectNone}>全不选</button>
          </div>
          <ul className="landmark-guild-filter-list">
            {options.map((opt) => {
              const checked = enabledGuildIDs == null || enabledGuildIDs.has(opt.id);
              return (
                <li key={opt.id}>
                  <label className="landmark-guild-filter-item">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => toggleGuild(opt.id)}
                    />
                    <span className="landmark-guild-filter-name">{opt.name}</span>
                    <span className="landmark-guild-filter-meta">{opt.count} 处</span>
                  </label>
                </li>
              );
            })}
          </ul>
          {options.length === 0 ? (
            <p className="landmark-guild-filter-empty">暂无公会据点数据（需存档或 game-data）</p>
          ) : null}
        </div>
      ) : null}
      {showGuildBases && options.length === 0 ? (
        <p className="landmark-guild-filter-empty landmark-guild-filter-empty--inline">
          暂无公会据点（配置存档 import 或 game-data）
        </p>
      ) : null}
    </div>
  );
}
