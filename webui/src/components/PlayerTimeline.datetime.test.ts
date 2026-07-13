import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { parseLocalDateTime } from './PlayerTimeline';

const originalTZ = process.env.TZ;

beforeAll(() => { process.env.TZ = 'America/New_York'; });
afterAll(() => {
  if (originalTZ === undefined) delete process.env.TZ;
  else process.env.TZ = originalTZ;
});

describe('parseLocalDateTime in a DST timezone', () => {
  it('rejects a nonexistent spring-forward wall time', () => {
    expect(parseLocalDateTime('2026-03-08T02:30')).toMatchObject({ error: expect.stringMatching(/不存在/) });
  });

  it('rejects an ambiguous fall-back wall time', () => {
    expect(parseLocalDateTime('2026-11-01T01:30')).toMatchObject({ error: expect.stringMatching(/歧义/) });
  });

  it('accepts an unambiguous local wall time', () => {
    const parsed = parseLocalDateTime('2026-03-08T03:30');
    expect(parsed.error).toBeUndefined();
    expect(parsed.date?.getFullYear()).toBe(2026);
    expect(parsed.date?.getHours()).toBe(3);
  });

  it('accepts a normal Asia/Shanghai wall time', () => {
    process.env.TZ = 'Asia/Shanghai';
    try {
      expect(parseLocalDateTime('2026-07-13T16:30').error).toBeUndefined();
    } finally {
      process.env.TZ = 'America/New_York';
    }
  });
});
