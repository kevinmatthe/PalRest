import type { PlayerProgressResponse, PlayerTimelineResponse, ProgressChange, ProgressMetricName } from '../api';

export type JourneyInput = {
  userID: string; start: number; end: number;
  timeline?: PlayerTimelineResponse; progress?: PlayerProgressResponse;
};
export type JourneyEdge = {
  start: number; end: number; durationMs: number; distance: number;
  from: { x: number; y: number; sourceRef: string };
  to: { x: number; y: number; sourceRef: string };
  stationary: boolean;
};
export type JourneyHeatCell = {
  id: string; x: number; y: number; durationMs: number;
  start: number; end: number; edges: JourneyEdge[];
};
export type JourneyTrendPoint = { checkpointID: number; time: number; value: number };
export type JourneyMetric = {
  status: 'known' | 'partial' | 'unknown' | 'boundary';
  delta: number | null; latestValue: number | null; changes: ProgressChange[];
  runs: JourneyTrendPoint[][]; reason?: string;
};
export type JourneyInference = {
  kind: 'exploration'; ruleVersion: 1;
  changeIDs: number[]; edges: JourneyEdge[];
  observedMs: number; movingMs: number; coverage: number; unlockedCount: number;
};
export type JourneySummary = {
  start: number; end: number;
  position: {
    sampleCount: number; totalCount: number; observedMs: number; unknownMs: number;
    coverage: number; movingMs: number; stationaryMs: number; pathLength: number;
    asOf: number | null; ageMs: number | null; edges: JourneyEdge[];
    level: { from: number; to: number; delta: number; start: number; end: number } | null;
  };
  metrics: Record<ProgressMetricName, JourneyMetric>;
  heat: JourneyHeatCell[]; inferences: JourneyInference[]; warnings: string[];
};
