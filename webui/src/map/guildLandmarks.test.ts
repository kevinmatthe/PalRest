import { describe, expect, it } from 'vitest';
import type { WorldPOI } from '../api';
import { filterGuildBases, guildOptionsFromPOIs } from './guildLandmarks';

const pois: WorldPOI[] = [
  { id: 'a', name_zh: '公会「狼」据点', kind: 'guild_base', x: 1, y: 2, guild_name: '狼', guild_id: 'g1' },
  { id: 'b', name_zh: '公会「狼」据点', kind: 'guild_base', x: 3, y: 4, guild_name: '狼', guild_id: 'g1' },
  { id: 'c', name_zh: '公会「狐」据点', kind: 'guild_base', x: 5, y: 6, guild_name: '狐', guild_id: 'g2' },
];

describe('guildLandmarks', () => {
  it('builds unique guild options with counts', () => {
    const opts = guildOptionsFromPOIs(pois);
    expect(opts).toHaveLength(2);
    expect(opts.find((o) => o.id === 'g1')).toEqual({ id: 'g1', name: '狼', count: 2 });
    expect(opts.find((o) => o.id === 'g2')).toEqual({ id: 'g2', name: '狐', count: 1 });
  });

  it('filters by enabled guild ids', () => {
    expect(filterGuildBases(pois, new Set(['g2']))).toHaveLength(1);
    expect(filterGuildBases(pois, new Set())).toHaveLength(0);
    expect(filterGuildBases(pois, null)).toHaveLength(3);
  });
});
