import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Player, Policies } from '../api';
import { PolicyManager } from './PolicyManager';

const policies: Policies = {
  version: 1,
  source: 'database',
  timezone: 'Asia/Shanghai',
  default: {
    enabled: true,
    strategy: 'fixed_window',
    period: 'daily',
    reset_at: '04:00',
    limit_ms: 7_200_000,
    cooldown_every_ms: 7_200_000,
    cooldown_rest_ms: 1_800_000,
    credit_recover_every_ms: 3_600_000,
    credit_recover_amount_ms: 1_800_000,
    credit_max_ms: 10_800_000,
    warning_before_ms: [1_800_000, 600_000],
  },
  overrides: {},
};

const players: Player[] = [{
  user_id: 'steam_1', player_id: 'one', name: 'Kevin', account_name: '', online: false,
  enabled: true, exempt: false, strategy: 'fixed_window', period: 'daily', used_ms: 0,
  remaining_ms: 7_200_000, limit_ms: 7_200_000, period_start: '', next_reset: '', warning_before_ms: [], warnings: [],
}];

describe('PolicyManager', () => {
  it('shows only fields for the selected strategy', async () => {
    const user = userEvent.setup();
    render(<PolicyManager policies={policies} players={players} busy={false} onSave={vi.fn()} onBack={() => {}} />);
    await user.selectOptions(screen.getByLabelText('策略'), 'cooldown');
    expect(screen.getByText('游玩时长')).toBeInTheDocument();
    expect(screen.getByText('强制休息')).toBeInTheDocument();
    // 策略下拉仍含「固定额度」选项文案，字段本身应已切换
  });

  it('shows reset weekday only for weekly fixed windows', async () => {
    const user = userEvent.setup();
    render(<PolicyManager policies={policies} players={players} busy={false} onSave={vi.fn()} onBack={() => {}} />);
    expect(screen.queryByLabelText('重置星期')).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('周期'), 'weekly');
    expect(screen.getByLabelText('重置星期')).toBeInTheDocument();
  });

  it('adds a known player override and keeps fields inherited', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<PolicyManager policies={policies} players={players} busy={false} onSave={onSave} onBack={() => {}} />);
    await user.click(screen.getByRole('button', { name: '添加覆盖' }));
    await user.selectOptions(screen.getByRole('combobox', { name: '已知玩家' }), 'steam_1');
    await user.click(screen.getByRole('button', { name: '创建覆盖' }));
    expect(screen.getByRole('heading', { name: 'Kevin' })).toBeInTheDocument();
    expect(screen.getByLabelText('启用状态')).toHaveValue('inherit');
    await user.click(screen.getByRole('button', { name: '保存策略' }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ overrides: { steam_1: { exempt: false } } }));
  });

  it('rejects a duplicate manual user ID', async () => {
    const user = userEvent.setup();
    const existing = { ...policies, overrides: { steam_1: { exempt: false } } };
    render(<PolicyManager policies={existing} players={players} busy={false} onSave={vi.fn()} onBack={() => {}} />);
    await user.click(screen.getByRole('button', { name: '添加覆盖' }));
    await user.click(screen.getByLabelText('手动输入用户 ID'));
    await user.type(screen.getByLabelText('用户 ID'), 'steam_1');
    await user.click(screen.getByRole('button', { name: '创建覆盖' }));
    expect(screen.getByRole('alert')).toHaveTextContent('该玩家已有覆盖规则');
  });

  it('adds unique warning thresholds', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    const withFiveMinutes = {
      ...policies,
      default: { ...policies.default, warning_before_ms: [1_800_000, 600_000, 300_000] },
    };
    render(<PolicyManager policies={withFiveMinutes} players={players} busy={false} onSave={onSave} onBack={() => {}} />);
    await user.click(screen.getByRole('button', { name: '添加阈值' }));
    await user.click(screen.getByRole('button', { name: '添加阈值' }));
    await user.click(screen.getByRole('button', { name: '保存策略' }));
    const saved = onSave.mock.calls[0][0] as { default: { warning_before_ms: number[] } };
    expect(new Set(saved.default.warning_before_ms).size).toBe(saved.default.warning_before_ms.length);
  });
});
