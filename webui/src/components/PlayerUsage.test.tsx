import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Player } from '../api';
import { PlayerUsage } from './PlayerUsage';

const player: Player = {
  user_id: 'steam_1', player_id: 'one', name: 'Kevin', account_name: '', online: true,
  enabled: true, exempt: false, strategy: 'credit', period: 'credit', used_ms: 900_000,
  remaining_ms: 2_700_000, credit_available_ms: 2_700_000, last_credit_recovered_ms: 900_000,
  limit_ms: 3_600_000, period_start: '', next_reset: '', warning_before_ms: [], warnings: [],
};

describe('PlayerUsage', () => {
  it('shows available credit and the most recently settled recovery', () => {
    render(<PlayerUsage player={player} />);
    expect(screen.getByText('45m available')).toBeInTheDocument();
    expect(screen.getByText('Last recovery +15m')).toBeInTheDocument();
  });

  it('distinguishes zero recovery from a missing display', () => {
    render(<PlayerUsage player={{ ...player, last_credit_recovered_ms: 0 }} />);
    expect(screen.getByText('No recovery recorded')).toBeInTheDocument();
  });
});
