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
});
