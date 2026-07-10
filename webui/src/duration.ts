export type DurationUnit = 'minutes' | 'hours';

export function toMilliseconds(value: number, unit: DurationUnit) {
  if (!Number.isFinite(value) || value <= 0) {
    throw new Error('Duration must be positive');
  }
  return Math.round(value * (unit === 'hours' ? 3_600_000 : 60_000));
}

export function fromMilliseconds(milliseconds: number) {
  return milliseconds > 0 && milliseconds % 3_600_000 === 0
    ? { value: milliseconds / 3_600_000, unit: 'hours' as const }
    : { value: milliseconds / 60_000, unit: 'minutes' as const };
}
