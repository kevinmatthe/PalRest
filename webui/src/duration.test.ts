import { describe, expect, it } from 'vitest';
import { fromMilliseconds, toMilliseconds } from './duration';

describe('duration conversion', () => {
  it('converts hours to integer milliseconds', () => {
    expect(toMilliseconds(1.5, 'hours')).toBe(5_400_000);
  });

  it('chooses hours for exact hour values', () => {
    expect(fromMilliseconds(7_200_000)).toEqual({ value: 2, unit: 'hours' });
  });

  it('chooses minutes otherwise', () => {
    expect(fromMilliseconds(5_400_000)).toEqual({ value: 90, unit: 'minutes' });
  });

  it('rejects non-positive values', () => {
    expect(() => toMilliseconds(0, 'minutes')).toThrow('positive');
  });
});
