import { useCallback, useRef } from 'react';
import {
  DEFAULT_HORIZON_MS,
  endFromScrubber,
  isPinnedToNow,
  matchPreset,
  scrubberPosition,
  WINDOW_PRESETS,
  windowFromEnd,
  windowTrackLeft,
  windowTrackWidth,
  type WindowPreset,
} from '../map/timeWindow';
import { formatTimelineDateTime } from './timelineShared';

export type TimeWindowControlProps = {
  start: string;
  end: string;
  /** Local datetime-local values → update both. */
  onRangeChange: (start: string, end: string) => void;
  parseLocal: (value: string) => { date?: Date; error?: string };
  toLocalInput: (date: Date) => string;
  showExact?: boolean;
  onShowExactChange?: (open: boolean) => void;
  /** Dense horizontal toolbar layout. */
  compact?: boolean;
};

function readRange(
  start: string,
  end: string,
  parseLocal: TimeWindowControlProps['parseLocal'],
): { startMs: number; endMs: number; windowMs: number } | null {
  const s = parseLocal(start).date;
  const e = parseLocal(end).date;
  if (!s || !e) return null;
  const startMs = s.getTime();
  const endMs = e.getTime();
  if (!(endMs > startMs)) return null;
  return { startMs, endMs, windowMs: endMs - startMs };
}

export function TimeWindowControl({
  start,
  end,
  onRangeChange,
  parseLocal,
  toLocalInput,
  showExact = false,
  onShowExactChange,
  compact = false,
}: TimeWindowControlProps) {
  const trackRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<{ pointerId: number; grabOffset: number } | null>(null);
  const nowMs = Date.now();
  const range = readRange(start, end, parseLocal);
  const windowMs = range?.windowMs ?? WINDOW_PRESETS[2]!.ms;
  const endMs = range?.endMs ?? nowMs;
  const startMs = range?.startMs ?? nowMs - windowMs;
  const activePreset = matchPreset(windowMs);
  const pinned = isPinnedToNow(endMs, nowMs);
  const left = windowTrackLeft(startMs, nowMs);
  const width = windowTrackWidth(windowMs);
  const position = scrubberPosition(endMs, windowMs, nowMs);

  const applyEndAndWindow = useCallback(
    (nextEndMs: number, nextWindowMs: number) => {
      const w = windowFromEnd(nextEndMs, nextWindowMs, Date.now());
      onRangeChange(toLocalInput(new Date(w.startMs)), toLocalInput(new Date(w.endMs)));
    },
    [onRangeChange, toLocalInput],
  );

  function applyPreset(preset: WindowPreset) {
    applyEndAndWindow(Date.now(), preset.ms);
  }

  function pinToNow() {
    applyEndAndWindow(Date.now(), windowMs);
  }

  function setFromTrackClientX(clientX: number, grabOffset = width / 2) {
    const track = trackRef.current;
    if (!track) return;
    const rect = track.getBoundingClientRect();
    if (rect.width <= 0) return;
    // Position is left edge of window on track (0..1), convert to end via scrubber of window center/end.
    const rawLeft = (clientX - rect.left) / rect.width - grabOffset;
    const maxLeft = 1 - width;
    const nextLeft = Math.min(maxLeft, Math.max(0, rawLeft));
    // left = (start - (now-horizon)) / horizon → start = horizonStart + left*horizon
    // end = start + windowMs → map to scrubber position via end
    const now = Date.now();
    const horizonStart = now - DEFAULT_HORIZON_MS;
    const nextStart = horizonStart + nextLeft * DEFAULT_HORIZON_MS;
    const nextEnd = nextStart + windowMs;
    applyEndAndWindow(nextEnd, windowMs);
  }

  function onThumbPointerDown(event: React.PointerEvent<HTMLButtonElement>) {
    const track = trackRef.current;
    if (!track) return;
    event.preventDefault();
    const rect = track.getBoundingClientRect();
    const grabOffset = rect.width > 0 ? (event.clientX - rect.left) / rect.width - left : width / 2;
    dragRef.current = { pointerId: event.pointerId, grabOffset };
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function onThumbPointerMove(event: React.PointerEvent<HTMLButtonElement>) {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    setFromTrackClientX(event.clientX, drag.grabOffset);
  }

  function onThumbPointerUp(event: React.PointerEvent<HTMLButtonElement>) {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      /* already released */
    }
  }

  function onTrackPointerDown(event: React.PointerEvent<HTMLDivElement>) {
    if ((event.target as HTMLElement).closest('.timeline-window-thumb')) return;
    setFromTrackClientX(event.clientX, width / 2);
  }

  return (
    <div className={`timeline-window-control${compact ? ' timeline-window-control--compact' : ''}`}>
      <div className="timeline-presets" role="group" aria-label="观察窗口长度">
        {WINDOW_PRESETS.map((preset) => (
          <button
            type="button"
            key={preset.id}
            aria-pressed={activePreset?.id === preset.id}
            onClick={() => applyPreset(preset)}
          >
            {preset.shortLabel}
          </button>
        ))}
        <button
          type="button"
          className="timeline-window-pin"
          disabled={pinned}
          onClick={pinToNow}
          title="将窗口右端对齐到当前时间"
        >
          贴现在
        </button>
      </div>

      <div className="timeline-window-scrubber">
        <div className="timeline-window-scrubber-labels">
          <span>更早</span>
          <span className="timeline-window-summary">
            {formatTimelineDateTime(new Date(startMs).toISOString())}
            {' → '}
            {formatTimelineDateTime(new Date(endMs).toISOString())}
            {pinned ? ' · 贴现在' : ''}
          </span>
          <span>现在</span>
        </div>
        <div
          className="timeline-window-track"
          ref={trackRef}
          onPointerDown={onTrackPointerDown}
          role="presentation"
        >
          <button
            type="button"
            className="timeline-window-thumb"
            style={{ left: `${left * 100}%`, width: `${width * 100}%` }}
            aria-label="拖动观察时间窗口"
            onPointerDown={onThumbPointerDown}
            onPointerMove={onThumbPointerMove}
            onPointerUp={onThumbPointerUp}
            onPointerCancel={onThumbPointerUp}
          >
            <span className="timeline-window-thumb-label">
              {activePreset?.shortLabel ?? '自定义'}
            </span>
          </button>
        </div>
        <label className="timeline-window-slider">
          <span className="sr-only">时间窗口位置</span>
          <input
            type="range"
            min={0}
            max={1000}
            step={1}
            value={Math.round(position * 1000)}
            aria-valuetext={`${formatTimelineDateTime(new Date(startMs).toISOString())} 至 ${formatTimelineDateTime(new Date(endMs).toISOString())}`}
            onChange={(event) => {
              const t = Number(event.target.value) / 1000;
              applyEndAndWindow(endFromScrubber(t, windowMs, Date.now()), windowMs);
            }}
          />
        </label>
      </div>

      <div className="timeline-window-advanced">
        <button
          type="button"
          className="timeline-window-exact-toggle"
          aria-expanded={showExact}
          onClick={() => onShowExactChange?.(!showExact)}
        >
          {showExact ? '收起精确时间' : '精确时间…'}
        </button>
        {showExact ? (
          <div className="timeline-window-exact-fields">
            <label className="timeline-field">
              <span>开始</span>
              <input
                type="datetime-local"
                value={start}
                onChange={(event) => onRangeChange(event.target.value, end)}
              />
            </label>
            <label className="timeline-field">
              <span>结束</span>
              <input
                type="datetime-local"
                value={end}
                onChange={(event) => onRangeChange(start, event.target.value)}
              />
            </label>
          </div>
        ) : null}
      </div>
      <p className="timeline-range-note">拖动窗口平移区间 · 本地时间 · 最长 31 天 · 每类最多 500 条</p>
    </div>
  );
}
