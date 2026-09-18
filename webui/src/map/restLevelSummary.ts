import type { JourneyLevelRun, JourneySummary } from './journeyTypes';
import { connectionBreak, type PreparedSample } from './workspacePlayback';

/** Sum witnessed changes, retaining every comparable run instead of only the final one. */
export function summarizeRestLevels(samples: PreparedSample[], userID: string, start: number, end: number, truncated: boolean): JourneySummary['position']['level'] {
  const points = samples.filter(p => p.time >= start && p.time <= end);
  const runs: JourneyLevelRun[] = [];
  let current: JourneyLevelRun | undefined;
  let partial = truncated;
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1], b = points[i];
    if (a.user_id !== userID || b.user_id !== userID || connectionBreak(a, b) ||
      !Number.isSafeInteger(a.level) || a.level < 1 || !Number.isSafeInteger(b.level) || b.level < 1 || b.level < a.level) {
      current = undefined; partial = true; continue;
    }
    if (current && current.end === a.time) {
      current.to = b.level; current.delta = b.level - current.from; current.end = b.time; current.sourceTo = b.source_ref;
    } else {
      current = { from: a.level, to: b.level, delta: b.level - a.level, start: a.time, end: b.time, sourceFrom: a.source_ref, sourceTo: b.source_ref };
      runs.push(current);
    }
  }
  if (!runs.length) return null;
  return { from: runs[0].from, to: runs.at(-1)!.to, start: runs[0].start, end: runs.at(-1)!.end,
    delta: runs.reduce((sum, run) => sum + run.delta, 0), partial, runs };
}
