import type { WorldPOI } from '../api';

export type GuildOption = {
  id: string;
  name: string;
  count: number;
};

/** Unique guilds from catalog for filter checkboxes. */
export function guildOptionsFromPOIs(pois: WorldPOI[]): GuildOption[] {
  const byID = new Map<string, GuildOption>();
  for (const poi of pois) {
    if (poi.kind !== 'guild_base') continue;
    const id = (poi.guild_id || poi.guild_name || poi.id).trim();
    if (!id) continue;
    const name = (poi.guild_name || id).trim();
    const prev = byID.get(id);
    if (prev) {
      prev.count += 1;
      continue;
    }
    byID.set(id, { id, name, count: 1 });
  }
  return [...byID.values()].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN') || a.id.localeCompare(b.id));
}

export function filterGuildBases(
  pois: WorldPOI[],
  enabledGuildIDs: ReadonlySet<string> | null,
): WorldPOI[] {
  if (!enabledGuildIDs) return pois.filter((p) => p.kind === 'guild_base');
  if (enabledGuildIDs.size === 0) return [];
  return pois.filter((p) => {
    if (p.kind !== 'guild_base') return false;
    const id = (p.guild_id || p.guild_name || p.id).trim();
    return enabledGuildIDs.has(id);
  });
}
