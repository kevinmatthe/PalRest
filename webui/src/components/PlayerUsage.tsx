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
            <strong>可用 {formatDuration(available)}</strong>
            <span className="credit-recovery-copy">
              {player.last_credit_recovered_ms
                ? `上次恢复 +${formatDuration(player.last_credit_recovered_ms)}`
                : '尚无恢复记录'}
            </span>
          </>
        ) : (
          <>
            <span>{formatDuration(player.used_ms)}</span>
            <span>剩余 {formatDuration(player.remaining_ms)}</span>
          </>
        )}
      </div>
      <div className="progress" aria-label={`已用 ${usage}%`}>
        <span style={{ width: `${usage}%` }} />
      </div>
    </div>
  );
}
