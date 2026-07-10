import type { Player } from '../api';
import { formatDuration, percent } from '../utils';

export function PlayerUsage({ player }: { player: Player }) {
  const credit = player.strategy === 'credit';
  const available = player.credit_available_ms ?? player.remaining_ms;
  const limit = credit ? player.used_ms + available : player.limit_ms;
  const usage = percent(player.used_ms, limit);

  return (
    <div className={`usage-cell${credit ? ' credit-usage' : ''}`}>
      <div className="usage-label">
        {credit ? (
          <>
            <strong>{formatDuration(available)} available</strong>
            <span className="credit-recovery-copy">
              {player.last_credit_recovered_ms
                ? `Last recovery +${formatDuration(player.last_credit_recovered_ms)}`
                : 'No recovery recorded'}
            </span>
          </>
        ) : (
          <>
            <span>{formatDuration(player.used_ms)}</span>
            <span>{formatDuration(player.remaining_ms)} left</span>
          </>
        )}
      </div>
      <div className="progress" aria-label={`${usage}% used`}>
        <span style={{ width: `${usage}%` }} />
      </div>
    </div>
  );
}
