import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { BehaviorSummaryPanel } from './BehaviorSummaryPanel';
import type { BehaviorSummary } from '../behavior/behaviorTypes';

function summary(partial: Partial<BehaviorSummary> = {}): BehaviorSummary {
  return {
    sampleCount: 10,
    segmentCount: 1,
    windowMs: 3600_000,
    observedActiveMs: 1800_000,
    pathLength: 50_000,
    radius: 12_000,
    meanSpeed: 40,
    peakSpeed: 900,
    sampleDensityPerHour: 20,
    classMs: { stationary: 600_000, local: 600_000, traveling: 600_000 },
    classShare: { stationary: 1 / 3, local: 1 / 3, traveling: 1 / 3 },
    gapMs: 0,
    gapShareOfWindow: 0,
    dominantClass: 'traveling',
    edges: [],
    poiDwells: [],
    teleportSuspects: [],
    poiHitRate: 0,
    ...partial,
  };
}

describe('BehaviorSummaryPanel', () => {
  it('renders empty when no samples and not loading', () => {
    render(<BehaviorSummaryPanel loading={false} selected summary={summary({ sampleCount: 0 })} />);
    expect(screen.getByText(/当前范围无位置样本/)).toBeInTheDocument();
  });

  it('renders mix labels and metrics when summary has samples', () => {
    render(<BehaviorSummaryPanel loading={false} selected summary={summary()} />);
    expect(screen.getByRole('region', { name: /行为摘要/ })).toBeInTheDocument();
    expect(screen.getByText('跑图')).toBeInTheDocument();
    expect(screen.getByText(/局部/)).toBeInTheDocument();
    expect(screen.getByText(/挂机/)).toBeInTheDocument();
    expect(screen.getByText(/观测活跃/)).toBeInTheDocument();
    expect(screen.getByText(/基于已加载的位置样本/)).toBeInTheDocument();
  });

  it('shows gap notice when gap share is high', () => {
    render(
      <BehaviorSummaryPanel
        loading={false}
        selected
        summary={summary({ gapShareOfWindow: 0.2, gapMs: 720_000 })}
      />,
    );
    expect(screen.getByText(/观测断档/)).toBeInTheDocument();
  });

  it('hides body when no player selected', () => {
    render(<BehaviorSummaryPanel loading={false} selected={false} summary={null} />);
    expect(screen.queryByRole('region', { name: /行为摘要/ })).not.toBeInTheDocument();
  });

  it('shows empty POI hint when no dwells or teleports', () => {
    render(<BehaviorSummaryPanel loading={false} selected summary={summary()} />);
    expect(screen.getByText(/未匹配到传送点、首领塔或公会据点附近的驻留/)).toBeInTheDocument();
  });

  it('renders activity anchor, dwells, guild presence, and teleports', () => {
    render(
      <BehaviorSummaryPanel
        loading={false}
        selected
        summary={summary({
          activityAnchor: {
            poiId: 'ft-a',
            nameZh: '中央 · 传送点 1',
            kind: 'fast_travel',
            dwellMs: 600_000,
            sampleHits: 5,
          },
          poiDwells: [
            {
              poiId: 'ft-a',
              nameZh: '中央 · 传送点 1',
              kind: 'fast_travel',
              dwellMs: 600_000,
              sampleHits: 5,
            },
            {
              poiId: 'tw-1',
              nameZh: '初始之塔',
              kind: 'boss_tower',
              dwellMs: 300_000,
              sampleHits: 3,
            },
            {
              poiId: 'gb-1',
              nameZh: '公会「狼」据点',
              kind: 'guild_base',
              dwellMs: 120_000,
              sampleHits: 2,
            },
          ],
          guildPresence: {
            guildName: '狼',
            baseCount: 2,
            dwellMs: 120_000,
          },
          teleportSuspects: [
            {
              fromNameZh: '中央 · 传送点 1',
              toNameZh: '火山 · 传送点 2',
              dist: 120_000,
              dtMs: 2_000,
              reason: 'long_jump',
              at: '2026-07-14T01:00:00Z',
            },
            {
              fromNameZh: '火山 · 传送点 2',
              toNameZh: '中央 · 传送点 1',
              dist: 120_000,
              dtMs: 10 * 60_000,
              reason: 'gap_hop',
              at: '2026-07-14T02:00:00Z',
            },
          ],
        })}
      />,
    );

    expect(screen.getByText('活动锚点')).toBeInTheDocument();
    expect(screen.getByText('驻留')).toBeInTheDocument();
    expect(screen.getByText('疑似传送')).toBeInTheDocument();

    expect(screen.getAllByText('中央 · 传送点 1').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('初始之塔')).toBeInTheDocument();
    expect(screen.getByText('公会「狼」据点')).toBeInTheDocument();

    expect(screen.getAllByText('传送点').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('首领塔')).toBeInTheDocument();
    // kind badge + section heading both use 公会据点
    expect(screen.getAllByText('公会据点').length).toBeGreaterThanOrEqual(2);

    expect(screen.getByText(/公会据点停留/)).toBeInTheDocument();
    expect(screen.getByText(/2 处/)).toBeInTheDocument();
    expect(screen.getByText(/· 狼/)).toBeInTheDocument();

    expect(screen.getByText('中央 · 传送点 1 → 火山 · 传送点 2')).toBeInTheDocument();
    expect(screen.getByText('大跳')).toBeInTheDocument();
    expect(screen.getByText('跨段')).toBeInTheDocument();

    expect(
      screen.queryByText(/未匹配到传送点、首领塔或公会据点附近的驻留/),
    ).not.toBeInTheDocument();
  });
});
