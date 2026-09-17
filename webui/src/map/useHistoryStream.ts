import { useEffect, useRef, useState } from 'react';
import { getPlayerTimeline, type PlayerTimelineResponse } from '../api';

const CHUNK_MS = 600000;
const OVERLAP_MS = 330000;
const MAX_REQUESTS = 16;
const MAX_ROWS = 10000;
const REQUEST_TIMEOUT_MS = 20000;

type HistoryStream = {
  data?: PlayerTimelineResponse;
  loading: boolean;
  error?: string;
  buffering: boolean;
  rangeStart?: number;
  rangeEnd?: number;
  loadedStart?: number;
  loadedEnd?: number;
};
type State = HistoryStream & { key: string; index?: number; complete?: boolean };
type Chunk = { start: number; end: number; data: PlayerTimelineResponse };
type Task = { index?: number; controller: AbortController; timedOut: boolean };

type MetadataResponse = PlayerTimelineResponse & { range_start?: string; range_end?: string };
function truncated(data: PlayerTimelineResponse) {
  return (data.trajectory_total ?? data.trajectories.length) > data.trajectories.length ||
    (data.event_total ?? data.events.length) > data.events.length ||
    (data.private_sample_total ?? data.private_samples.length) > data.private_samples.length;
}
function merge(userID: string, responses: PlayerTimelineResponse[]): PlayerTimelineResponse {
  const trajectories = new Map<string, PlayerTimelineResponse['trajectories'][number]>();
  const events = new Map<string, PlayerTimelineResponse['events'][number]>();
  const privateSamples = new Map<string, PlayerTimelineResponse['private_samples'][number]>();
  for (const data of responses) {
    for (const row of data.trajectories) trajectories.set(JSON.stringify([row.user_id, row.observed_at, row.source_ref]), row);
    for (const row of data.events) events.set(row.id, row);
    for (const row of data.private_samples) privateSamples.set(JSON.stringify([row.user_id, row.observed_at, row.source_ref]), row);
  }
  return {
    user_id: userID,
    trajectories: [...trajectories.values()].sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at) || a.source_ref.localeCompare(b.source_ref)),
    events: [...events.values()].sort((a, b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at) || a.id.localeCompare(b.id)),
    private_samples: [...privateSamples.values()].sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at)),
    trajectory_total: trajectories.size, event_total: events.size, private_sample_total: privateSamples.size,
  };
}
function targetIndex(cursor: number | null, start: number, end: number) {
  return Math.floor(Math.max(start, Math.min(end, cursor !== null && Number.isFinite(cursor) ? cursor : start)) / CHUNK_MS);
}

/** One session owns metadata, a serial loader, and at most three complete chunks. */
class HistorySession {
  private stopped = false;
  private bootstrapped = false;
  private working = false;
  private task?: Task;
  private chunks = new Map<number, Chunk>();
  private errors = new Map<number, string>();
  private bootstrapError?: string;
  private complete?: PlayerTimelineResponse;
  private hasRange = false;
  private rangeStart: number;
  private rangeEnd: number;

  constructor(private key: string, private userID: string, private start: number, private end: number,
    private cursor: number | null, private update: (state: State) => void) {
    this.rangeStart = start; this.rangeEnd = end;
  }
  private get index() { return targetIndex(this.cursor ?? this.rangeStart, this.start, this.end); }

  seek(cursor: number | null) {
    this.cursor = cursor;
    const pending = this.task?.index;
    if (pending !== undefined && pending !== this.index && (!this.chunks.has(this.index) || pending !== this.index + 1)) this.task?.controller.abort();
    this.publish();
    void this.pump();
  }
  stop() { this.stopped = true; this.task?.controller.abort(); }

  private publish() {
    if (this.stopped) return;
    const state: State = { key: this.key, index: this.index, loading: !this.bootstrapped && !this.bootstrapError,
      buffering: !this.complete && !this.chunks.has(this.index), error: this.bootstrapError ?? this.errors.get(this.index) ?? this.errors.get(this.index + 1),
      rangeStart: this.bootstrapped && this.hasRange ? this.rangeStart : undefined,
      rangeEnd: this.bootstrapped && this.hasRange ? this.rangeEnd : undefined, complete: !!this.complete };
    if (this.complete) {
      state.data = this.complete; state.loadedStart = this.start; state.loadedEnd = this.end;
    } else if (this.chunks.has(this.index)) {
      let first = this.index, last = this.index;
      while (this.chunks.has(first - 1)) first--;
      while (this.chunks.has(last + 1)) last++;
      const contiguous: PlayerTimelineResponse[] = [];
      for (let index = first; index <= last; index++) contiguous.push(this.chunks.get(index)!.data);
      state.data = merge(this.userID, contiguous);
      state.loadedStart = this.chunks.get(first)!.start;
      state.loadedEnd = this.chunks.get(last)!.end;
    }
    this.update(state);
  }

  private async request(start: number, end: number, limit: number, task: Task): Promise<PlayerTimelineResponse> {
    if (this.stopped || task.controller.signal.aborted) throw new Error('历史请求已取消');
    const timeout = window.setTimeout(() => { task.timedOut = true; task.controller.abort(); }, REQUEST_TIMEOUT_MS);
    try {
      const data = await getPlayerTimeline(this.userID, new Date(start).toISOString(), new Date(end).toISOString(), limit, task.controller.signal);
      if (task.timedOut) throw new Error('历史请求超时，请重试');
      if (data.user_id !== this.userID) throw new Error('历史响应玩家不匹配');
      return data;
    } finally { window.clearTimeout(timeout); }
  }

  async startLoading() {
    this.publish();
    const task: Task = { controller: new AbortController(), timedOut: false };
    this.task = task;
    try {
      const data: MetadataResponse = await this.request(this.start, this.end, 1, task);
      if (this.stopped || task.controller.signal.aborted) return;
      const incomplete = truncated(data);
      const observations = [...data.trajectories.map(row => Date.parse(row.observed_at)),
        ...data.events.map(row => Date.parse(row.occurred_at)), ...data.private_samples.map(row => Date.parse(row.observed_at))]
        .filter(time => Number.isFinite(time) && time >= this.start && time <= this.end);
      this.hasRange = incomplete || observations.length > 0;
      let first = Date.parse(data.range_start ?? ''), last = Date.parse(data.range_end ?? '');
      if (!incomplete && observations.length && (!Number.isFinite(first) || !Number.isFinite(last))) {
        first = Math.min(...observations); last = Math.max(...observations);
      }
      if (Number.isFinite(first) && Number.isFinite(last) && first <= last && first <= this.end && last >= this.start) {
        this.rangeStart = Math.max(this.start, first); this.rangeEnd = Math.min(this.end, last);
      }
      if (!incomplete) this.complete = merge(this.userID, [data]);
      this.bootstrapped = true;
    } catch (error) {
      if (!this.stopped) this.bootstrapError = task.timedOut ? '历史请求超时，请重试' : error instanceof Error ? error.message : '历史记录暂时不可用';
    } finally {
      this.task = undefined;
      this.publish();
      void this.pump();
    }
  }

  private async loadChunk(index: number, task: Task): Promise<Chunk> {
    const start = Math.max(this.start, index * CHUNK_MS - OVERLAP_MS);
    const end = Math.min(this.end, (index + 1) * CHUNK_MS + OVERLAP_MS);
    let requests = 0, rows = 0;
    const parts: PlayerTimelineResponse[] = [];
    const load = async (from: number, to: number, limit = 500): Promise<void> => {
      if (++requests > MAX_REQUESTS) throw new Error('该时段历史过密，超过分段请求上限，请缩小时间范围');
      const data = await this.request(from, to, limit, task);
      if (this.stopped || task.controller.signal.aborted) throw new Error('历史请求已取消');
      rows += data.trajectories.length + data.events.length + data.private_samples.length;
      if (rows > MAX_ROWS) throw new Error('该时段历史过密，超过记录上限，请缩小时间范围');
      if (!truncated(data)) { parts.push(data); return; }
      if (to - from <= 1) {
        if (limit === 2000) throw new Error('同一时间点历史超过2000条，无法完整加载，请缩小范围或导出');
        await load(from, to, 2000);
        return;
      }
      const middle = Math.floor((from + to) / 2);
      // Adjacent half-open ranges preserve every timestamp; merge also deduplicates response overlaps.
      await load(from, middle);
      await load(middle, to);
    };
    await load(start, end);
    return { start, end, data: merge(this.userID, parts) };
  }

  private async pump() {
    if (this.stopped || !this.bootstrapped || this.complete || this.working) return;
    const desired = this.index;
    const index = !this.chunks.has(desired) ? desired : desired + 1;
    if (index * CHUNK_MS > this.end || this.chunks.has(index) || this.errors.has(index)) return;
    this.working = true;
    const task: Task = { index, controller: new AbortController(), timedOut: false };
    this.task = task;
    try {
      const chunk = await this.loadChunk(index, task);
      if (this.stopped || task.controller.signal.aborted) return;
      this.chunks.set(index, chunk);
      while (this.chunks.size > 3) {
        const farthest = [...this.chunks.keys()].sort((a, b) => Math.abs(b - this.index) - Math.abs(a - this.index) || a - b)[0];
        this.chunks.delete(farthest);
      }
    } catch (error) {
      if (!this.stopped && (!task.controller.signal.aborted || task.timedOut)) {
        this.errors.set(index, task.timedOut ? '历史请求超时，请重试' : error instanceof Error ? error.message : '历史片段暂时不可用');
      }
    } finally {
      this.working = false; this.task = undefined;
      this.publish();
      void this.pump();
    }
  }
}

/** Bounded replay history: metadata first, then cursor-local chunks plus one prefetch. */
export function useHistoryStream(active: boolean, userID: string, start: number, end: number, cursor: number | null, revision = 0): HistoryStream {
  const key = JSON.stringify([userID, start, end, revision]);
  const valid = !!userID && Number.isFinite(start) && Number.isFinite(end) && start <= end &&
    Math.abs(start) <= 8640000000000000 && Math.abs(end) <= 8640000000000000;
  const [state, setState] = useState<State>({ key: '', loading: false, buffering: false });
  const session = useRef<HistorySession | null>(null);
  const latestCursor = useRef(cursor);
  latestCursor.current = cursor;
  useEffect(() => {
    if (!active || !valid) return;
    const next = new HistorySession(key, userID, start, end, latestCursor.current, setState);
    session.current = next;
    void next.startLoading();
    return () => { next.stop(); if (session.current === next) session.current = null; };
  }, [active, valid, key, userID, start, end]);
  useEffect(() => { session.current?.seek(cursor); }, [cursor]);
  if (!active || !valid) return { loading: false, buffering: false };
  if (state.key !== key) return { loading: true, buffering: true };
  if (!state.complete && state.index !== targetIndex(cursor ?? state.rangeStart ?? start, start, end)) {
    return { loading: state.loading, buffering: true, rangeStart: state.rangeStart, rangeEnd: state.rangeEnd };
  }
  return state;
}
