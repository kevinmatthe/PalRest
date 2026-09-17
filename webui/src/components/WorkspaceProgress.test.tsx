import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { PlayerProgressResponse } from '../api';
import { WorkspaceProgress } from './WorkspaceProgress';
const data: PlayerProgressResponse = { user_id: 'u', status: 'available', baseline: null, checkpoints: [{ id: 1, world_id: 'w', observed_at: '2026-09-16T10:00:00Z', captured_at: '2026-09-16T10:01:00Z', source: 'save_import', schema_version: 1, consistent: true, boundary: '', metrics: { owned_pals: { state: 'known', value: 45 }, capture_total: { state: 'unknown' }, paldeck: { state: 'unknown' }, fast_travel: { state: 'unsupported' } } }], changes: [{ id: 1, metric: 'owned_pals', before: 42, after: 45, delta: 3, added: [], removed: [], interval_start: '2026-09-16T09:50:00Z', interval_end: '2026-09-16T10:00:00Z', checkpoint_id: 1, previous_checkpoint_id: 0, rule_version: 1, confidence: 'observed', source: 'save_import' }], checkpoint_total: 1, change_total: 8 };
it('shows observed quantities, explains truncation and seeks a change interval without claiming captures or a location', () => {
  const onChange = vi.fn();
  render(<WorkspaceProgress name="测试玩家" data={data} mode="history" cursor={Date.parse('2026-09-16T10:00:00Z')} loading={false} onChange={onChange} onRetry={() => {}} />);
  expect(screen.getByText('45')).toBeInTheDocument();
  expect(screen.getAllByText('未采集')).toHaveLength(4);
  expect(screen.getByText('暂不支持')).toBeInTheDocument();
  expect(screen.getByText(/变化仅加载 1\/8/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /拥有帕鲁 42 → 45/ }));
  expect(onChange).toHaveBeenCalledWith(data.changes[0]);
  expect(screen.queryByText(/捕获了/)).not.toBeInTheDocument();
});
it('does not reveal future quantities or change rows in history', () => {
  render(<WorkspaceProgress name="测试玩家" data={data} mode="history" cursor={Date.parse('2026-09-16T09:55:00Z')} loading={false} onChange={() => {}} onRetry={() => {}} />);
  expect(screen.queryByText('45')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /42 → 45/ })).not.toBeInTheDocument();
  expect(screen.getByText('此刻之前还没有进度观测')).toBeInTheDocument();
});
it('explains boundaries and inconsistent snapshots without presenting a behavior inference', () => {
  const bounded = { ...data, checkpoints: [{ ...data.checkpoints[0], boundary: 'rollback', consistent: false }], changes: [] };
  render(<WorkspaceProgress name="玩家" data={bounded} mode="live" cursor={Date.now()} loading={false} onChange={() => {}} onRetry={() => {}} />);
  expect(screen.getByText(/回档/)).toBeInTheDocument();
  expect(screen.getByText(/文件一致性未确认/)).toBeInTheDocument();
});
it('keeps shared guild pals separate from personal ownership', () => {
  const shared: PlayerProgressResponse = { ...data, checkpoints: [{ ...data.checkpoints[0], unattributed_pals: { state: 'known', value: 228 } }] };
  render(<WorkspaceProgress name="玩家" data={shared} mode="history" cursor={Date.parse('2026-09-16T10:00:00Z')} loading={false} onChange={() => {}} onRetry={() => {}} />);
  expect(screen.getByText('45')).toBeInTheDocument();
  expect(screen.getByText('拥有数仅统计已确认个人归属的帕鲁。')).toBeInTheDocument();
  expect(screen.getByText('另有 228 只公会帕鲁未确认个人归属，未计入拥有数。')).toBeInTheDocument();
});
it.each([undefined, { state: 'unknown' as const }, { state: 'known' as const }])('does not turn an incomplete attribution metric into zero (%j)', unattributed => {
  const shared: PlayerProgressResponse = { ...data, checkpoints: [{ ...data.checkpoints[0], unattributed_pals: unattributed }] };
  render(<WorkspaceProgress name="玩家" data={shared} mode="live" cursor={Date.now()} loading={false} onChange={() => {}} onRetry={() => {}} />);
  expect(screen.getByText('个人归属统计尚未完整，公会中未确认归属的帕鲁未计入拥有数。')).toBeInTheDocument();
  expect(screen.queryByText(/另有 0 只/)).not.toBeInTheDocument();
});
it('explains equal-count set replacements with their added and removed counts', () => {
  const replaced: PlayerProgressResponse = { ...data, changes: [{ ...data.changes[0], metric: 'paldeck', before: 12, after: 12, delta: 0, added: ['NewSpecies'], removed: ['OldSpecies'] }] };
  render(<WorkspaceProgress name="玩家" data={replaced} mode="live" cursor={Date.now()} loading={false} onChange={() => {}} onRetry={() => {}} />);
  expect(screen.getByRole('button', { name: /图鉴解锁 12 → 12/ })).toBeInTheDocument();
  expect(screen.getByText('新增 1 项 · 移除 1 项')).toBeInTheDocument();
  expect(screen.queryByText('NewSpecies')).not.toBeInTheDocument();
});
