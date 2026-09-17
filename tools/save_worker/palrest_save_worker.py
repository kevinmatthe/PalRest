#!/opt/palrest-save-worker/bin/python
"""Extract a minimal PalRest world snapshot from a Palworld Level.sav.

This worker intentionally stays outside the Go service. It depends on
PalworldSaveTools' ``palsav`` package at runtime and emits a compact JSON
contract that PalRest can import without carrying the parser implementation in
the main binary.
"""

from __future__ import annotations

import argparse
import contextlib
import datetime as dt
import gc
import hashlib
import json
import os
import re
import shutil
import tempfile
from pathlib import Path
from typing import Any

from palsav.core import decompress_sav_to_gvas
from palsav.gvas import GvasFile
from palsav.paltypes import PALWORLD_CUSTOM_PROPERTIES, PALWORLD_TYPE_HINTS


PARSER_NAME = "palrest-palsav-worker"
PARSER_VERSION = 2


@contextlib.contextmanager
def gc_paused():
    enabled = gc.isenabled()
    if enabled:
        gc.disable()
    try:
        yield
    finally:
        if enabled:
            gc.enable()
            gc.collect()


def main() -> int:
    parser = argparse.ArgumentParser(description="Extract PalRest save snapshot JSON")
    parser.add_argument("--level", required=True, help="Path to Level.sav")
    parser.add_argument("--world-id", help="Explicit original world GUID for relocated backups")
    parser.add_argument("--output", "-o", help="Output JSON path. Defaults to stdout.")
    parser.add_argument("--pretty", action="store_true", help="Pretty-print JSON output")
    args = parser.parse_args()

    level_path = Path(args.level)
    snapshot = extract_snapshot(level_path, args.world_id)
    encoded = json.dumps(snapshot, ensure_ascii=False, indent=2 if args.pretty else None, separators=None if args.pretty else (",", ":"))
    if args.output:
        Path(args.output).write_text(encoded + "\n", encoding="utf-8")
    else:
        print(encoded)
    return 0


def extract_snapshot(level_path: Path, world_id: str | None = None) -> dict[str, Any]:
    if level_path.name != "Level.sav":
        raise ValueError("input must be Level.sav")
    identity, identity_kind = world_identity(level_path, world_id)
    captured_at = utc_now()
    catalog_bytes = Path(__file__).with_name("character_ids.json").read_bytes()
    progress_definition = hashlib.sha256(catalog_bytes).hexdigest()
    catalog = json.loads(catalog_bytes)
    with stable_snapshot(level_path) as (copy_path, manifest), gc_paused():
        level_gvas = read_gvas(copy_path)
        world = level_gvas.properties["worldSaveData"]["value"]
        generation = save_generation(level_gvas.properties)
        consistent = generation is not None
        reason = 'matching_save_timestamps' if consistent else 'save_timestamp_missing'
        meta_path = copy_path.parent / 'LevelMeta.sav'
        if meta_path.exists():
            try:
                meta = read_gvas(meta_path).properties
                if save_generation(meta) != generation:
                    consistent, reason = False, 'save_generation_mismatch'
            except Exception:
                consistent, reason = False, 'metadata_parse_failed'
        else:
            consistent, reason = False, 'metadata_missing'
        player_data, failures = {}, {}
        for path in sorted((copy_path.parent / 'Players').glob('*.sav')):
            uid = strict_guid(path.stem)
            if not uid:
                consistent, reason = False, 'player_identity_unknown'
                continue
            try:
                props = read_gvas(path).properties
                data = value_at(props, 'SaveData', 'value')
                if not isinstance(data, dict) or strict_guid(value_at(data, 'PlayerUId', 'value')) != uid:
                    failures[uid] = 'player_identity_mismatch'
                    consistent, reason = False, 'player_identity_mismatch'
                    continue
                player_data[uid] = data
                if save_generation(props) != generation:
                    consistent, reason = False, 'save_generation_mismatch'
            except Exception:
                failures[uid] = 'player_parse_failed'
                consistent, reason = False, 'player_parse_failed'
        level_stat = manifest['Level.sav']
        mtime = level_stat[3] / 1_000_000_000
        real_ticks = value_at(world, "GameTimeSaveData", "value", "RealDateTimeTicks", "value") or 0
        players = extract_players(world, real_ticks, mtime)
        owned = owned_metrics(world, player_data, catalog)
        unattributed = unattributed_metrics(world, [p['save_player_hex'] for p in players], catalog)
        for player in players:
            uid = player['save_player_hex']
            data = player_data.get(uid)
            if data is None:
                metrics = {name: unknown(failures.get(uid, 'player_file_missing')) for name in ('owned_pals', 'capture_total', 'paldeck', 'fast_travel')}
                consistent, reason = False, failures.get(uid, 'player_file_missing')
            else:
                metrics = {'owned_pals': owned[uid]}
                for name, field in [('capture_total', 'PalCaptureCount'), ('paldeck', 'PaldeckUnlockFlag'), ('fast_travel', 'FastTravelPointUnlockFlag')]:
                    metrics[name] = record_metric(data, field)
            player['progress'] = {'schema_version': 1, 'metrics': metrics, 'unattributed_pals': unattributed[uid]}
        return {
            "schema": "palrest.save_snapshot.v1",
            "parser": {"name": PARSER_NAME, "version": PARSER_VERSION},
            "source": {
                "level_sav": str(level_path), "fingerprint": snapshot_fingerprint(copy_path, copy_path.parent / 'Players', identity, identity_kind, progress_definition),
                "level_sav_size": level_stat[2], "level_sav_mtime": utc_from_timestamp(mtime),
                "captured_at": captured_at, "player_file_count": len(manifest) - 1 - int('LevelMeta.sav' in manifest),
                "world_id": identity, "world_id_kind": identity_kind,
                "source_time": utc_from_timestamp(mtime),
                "source_time_kind": 'file_mtime',
                "progress_definition": progress_definition,
                "consistent": consistent, "consistency_reason": reason,
            },
            "players": players, "guilds": extract_guilds(world, real_ticks, mtime),
        }


def read_gvas(path: Path) -> GvasFile:
    raw_gvas, _ = decompress_sav_to_gvas(path.read_bytes())
    return GvasFile.read(raw_gvas, PALWORLD_TYPE_HINTS, PALWORLD_CUSTOM_PROPERTIES)


def snapshot_fingerprint(level_path: Path, players_dir: Path, world_id: str = "", world_id_kind: str = "unknown", progress_definition: str | None = None) -> str:
    digest = hashlib.sha256(b"palrest-palsav-worker:2;progress:1\0")
    digest.update(json.dumps([world_id, world_id_kind], separators=(",", ":")).encode())
    if progress_definition is None:
        progress_definition = hashlib.sha256(Path(__file__).with_name("character_ids.json").read_bytes()).hexdigest()
    digest.update(bytes.fromhex(progress_definition))
    paths = [level_path, *sorted(p for p in players_dir.glob("*.sav") if not p.name.endswith("_dps.sav"))]
    meta = level_path.parent / "LevelMeta.sav"
    if meta.is_file():
        paths.append(meta)
    for path in paths:
        stat = path.stat()
        rel = path.relative_to(level_path.parent).as_posix()
        digest.update(rel.encode("utf-8"))
        digest.update(b"\0")
        digest.update(str(stat.st_size).encode("ascii"))
        digest.update(b"\0")
        with path.open("rb") as handle:
            for block in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(block)
        digest.update(b"\0")
    return digest.hexdigest()


def extract_players(world: dict[str, Any], real_ticks: int, file_mtime: float) -> list[dict[str, Any]]:
    entries = value_at(world, "CharacterSaveParameterMap", "value") or []
    by_uid: dict[str, dict[str, Any]] = {}
    for entry in entries:
        save_param = value_at(entry, "value", "RawData", "value", "object", "SaveParameter", "value")
        if not save_param or not value_at(save_param, "IsPlayer", "value"):
            continue
        raw_uid = value_at(entry, "key", "PlayerUId", "value")
        uid_hex = uid_to_hex(raw_uid)
        if not uid_hex:
            continue
        player = {
            "save_player_uid": hex_to_decimal_uid(uid_hex),
            "save_player_hex": uid_hex,
            "nickname": value_at(save_param, "NickName", "value") or "",
            "level": byte_value(save_param.get("Level"), 1),
            "exp": int(value_at(save_param, "Exp", "value") or 0),
            "hp": fixed_point(save_param.get("Hp")),
            "shield_hp": fixed_point(save_param.get("ShieldHP")),
            "full_stomach": round(float(value_at(save_param, "FullStomach", "value") or 0), 2),
            "save_last_online": "",
        }
        existing = by_uid.get(uid_hex)
        if existing is None or player["level"] > existing["level"]:
            by_uid[uid_hex] = player

    for member in iter_guild_members(world, real_ticks, file_mtime):
        player = by_uid.get(member["save_player_hex"])
        if player and member["last_online"]:
            player["save_last_online"] = member["last_online"]

    return sorted(by_uid.values(), key=lambda p: (-p["level"], p["nickname"], p["save_player_hex"]))


def extract_guilds(world: dict[str, Any], real_ticks: int, file_mtime: float) -> list[dict[str, Any]]:
    guild_entries = []
    groups = value_at(world, "GroupSaveDataMap", "value") or []
    base_camps = index_base_camps(world)
    for group in groups:
        group_type = value_at(group, "value", "GroupType", "value", "value")
        if group_type != "EPalGroupType::Guild":
            continue
        raw = value_at(group, "value", "RawData", "value")
        if not raw:
            continue
        base_ids = [uid_to_hex(value) for value in raw.get("base_ids", [])]
        members = []
        for member in raw.get("players", []):
            player_uid_hex = uid_to_hex(member.get("player_uid"))
            if not player_uid_hex:
                continue
            info = member.get("player_info") or {}
            last_online = ""
            if info.get("last_online_real_time"):
                last_online = tick_to_utc(info["last_online_real_time"], real_ticks, file_mtime)
            members.append({
                "save_player_uid": hex_to_decimal_uid(player_uid_hex),
                "save_player_hex": player_uid_hex,
                "nickname": info.get("player_name") or "",
                "last_online": last_online,
            })
        camps = []
        for base_id in base_ids:
            camp = base_camps.get(base_id)
            if camp:
                camps.append(camp)
        admin_hex = uid_to_hex(raw.get("admin_player_uid"))
        guild_entries.append({
            "save_guild_id": uid_to_hex(group.get("key")),
            "name": raw.get("guild_name") or "",
            "base_camp_level": int(raw.get("base_camp_level") or 0),
            "admin_save_player_uid": hex_to_decimal_uid(admin_hex),
            "admin_save_player_hex": admin_hex,
            "members": sorted(members, key=lambda m: (m["nickname"], m["save_player_hex"])),
            "base_camps": sorted(camps, key=lambda c: c["save_base_hex"]),
        })
    return sorted(guild_entries, key=lambda g: (-g["base_camp_level"], g["name"], g["save_guild_id"]))


def iter_guild_members(world: dict[str, Any], real_ticks: int, file_mtime: float):
    for group in value_at(world, "GroupSaveDataMap", "value") or []:
        if value_at(group, "value", "GroupType", "value", "value") != "EPalGroupType::Guild":
            continue
        raw = value_at(group, "value", "RawData", "value") or {}
        for member in raw.get("players", []):
            player_uid_hex = uid_to_hex(member.get("player_uid"))
            if not player_uid_hex:
                continue
            info = member.get("player_info") or {}
            last_online = ""
            if info.get("last_online_real_time"):
                last_online = tick_to_utc(info["last_online_real_time"], real_ticks, file_mtime)
            yield {
                "save_player_uid": hex_to_decimal_uid(player_uid_hex),
                "save_player_hex": player_uid_hex,
                "last_online": last_online,
            }


def index_base_camps(world: dict[str, Any]) -> dict[str, dict[str, Any]]:
    result = {}
    for entry in value_at(world, "BaseCampSaveData", "value") or []:
        raw = value_at(entry, "value", "RawData", "value")
        if not raw:
            continue
        base_hex = uid_to_hex(raw.get("id"))
        if not base_hex:
            continue
        transform = raw.get("transform") or {}
        translation = transform.get("translation") or {}
        group_hex = uid_to_hex(raw.get("group_id_belong_to"))
        result[base_hex] = {
            "save_base_uid": hex_to_decimal_uid(base_hex),
            "save_base_hex": base_hex,
            "save_group_uid": hex_to_decimal_uid(group_hex),
            "save_group_hex": group_hex,
            "area": float(raw.get("area_range") or 0),
            "location_x": float(translation.get("x") or 0),
            "location_y": float(translation.get("y") or 0),
            "location_z": float(translation.get("z") or 0),
        }
    return result


class UnstableSnapshotError(RuntimeError):
    pass


def source_manifest(level_path: Path) -> dict[str, tuple[int, ...]]:
    root = level_path.parent
    if not level_path.is_file() or not (root / 'Players').is_dir():
        raise FileNotFoundError('Level.sav and Players directory are required')
    paths = [level_path, *sorted((root / 'Players').glob('*.sav'))]
    if (root / 'LevelMeta.sav').exists():
        paths.append(root / 'LevelMeta.sav')
    result = {}
    for path in paths:
        if path.name.endswith('_dps.sav'):
            continue
        stat = path.stat()
        result[path.relative_to(root).as_posix()] = (stat.st_dev, stat.st_ino, stat.st_size, stat.st_mtime_ns, stat.st_ctime_ns)
    return result


@contextlib.contextmanager
def stable_snapshot(level_path: Path):
    """Copy all inputs before parsing; reject any changed file or file set."""
    before = source_manifest(level_path)
    with tempfile.TemporaryDirectory(prefix='palrest-save-') as directory:
        root = Path(directory)
        (root / 'Players').mkdir()
        for relative in before:
            shutil.copy2(level_path.parent / relative, root / relative)
        if source_manifest(level_path) != before:
            raise UnstableSnapshotError('source_changed_during_copy')
        yield root / 'Level.sav', before


def strict_guid(value: Any) -> str:
    text = str(value or '').replace('-', '').upper()
    return text if re.fullmatch(r'[0-9A-F]{32}', text) and text != '0' * 32 else ''


def world_identity(level_path: Path, explicit: str | None) -> tuple[str, str]:
    if explicit is not None:
        result = strict_guid(explicit)
        if not result:
            raise ValueError('world_id must be a nonzero 32-digit GUID')
        return result, 'explicit'
    for parent in level_path.absolute().parents:
        result = strict_guid(parent.name)
        if result:
            return result, 'directory'
    return '', 'unknown'


def save_generation(properties: dict) -> int | None:
    # Unreal Timestamp is a server-local wall clock in the validated saves.
    # Compare its raw ticks; it is not a UTC instant without a known timezone.
    ticks = value_at(properties, 'Timestamp', 'value')
    return ticks if type(ticks) is int and ticks > 0 else None


def unknown(reason: str, state: str = 'unknown') -> dict:
    return {'state': state, 'reason': reason}


def record_metric(save_data: dict, field: str) -> dict:
    record = value_at(save_data, 'RecordData', 'value')
    if not isinstance(record, dict) or field not in record:
        return unknown('field_missing')
    prop = record[field]
    counter = field == 'PalCaptureCount'
    expected = 'IntProperty' if counter else 'BoolProperty'
    if not isinstance(prop, dict) or prop.get('type') != 'MapProperty' or prop.get('key_type') != 'NameProperty' or prop.get('value_type') != expected:
        return unknown('field_shape_unsupported', 'unsupported')
    entries = prop.get('value')
    if not isinstance(entries, list):
        return unknown('invalid_field_value')
    seen, ids, total = set(), set(), 0
    for entry in entries:
        if not isinstance(entry, dict):
            return unknown('invalid_field_value')
        key, value = entry.get('key'), entry.get('value')
        if not isinstance(key, str) or not key:
            return unknown('invalid_or_duplicate_id')
        if field == 'FastTravelPointUnlockFlag':
            key = strict_guid(key)
        if not key or key.casefold() in seen:
            return unknown('invalid_or_duplicate_id')
        seen.add(key.casefold())
        if counter:
            if type(value) is not int or value < 0:
                return unknown('invalid_field_value')
            total += value
            if total > 9223372036854775807:
                return unknown('invalid_field_value')
        else:
            if type(value) is not bool:
                return unknown('invalid_field_value')
            if field == 'FastTravelPointUnlockFlag':
                key = strict_guid(key)
                if not key:
                    return unknown('invalid_field_value')
            if value:
                ids.add(key)
    if counter:
        return {'state': 'known', 'value': total}
    return {'state': 'known', 'value': len(ids), 'ids': sorted(ids)}


def character_containers(world: dict) -> tuple[set[str], dict[str, set[str]]]:
    """An absent or malformed Slots array is unknown, distinct from empty slots."""
    containers, invalid, membership = set(), set(), {}
    for entry in value_at(world, 'CharacterContainerSaveData', 'value') or []:
        cid = strict_guid(value_at(entry, 'key', 'ID', 'value'))
        slots = value_at(entry, 'value', 'Slots', 'value', 'values')
        if not isinstance(slots, list):
            invalid.add(cid)
            continue
        containers.add(cid)
        for slot in slots:
            raw_id = value_at(slot, 'RawData', 'value', 'instance_id')
            iid = strict_guid(raw_id)
            if iid:
                membership.setdefault(iid, set()).add(cid)
            elif str(raw_id).replace('-', '') != '0' * 32:
                invalid.add(cid)
    return containers - invalid, membership


def owned_metrics(world: dict, player_data: dict[str, dict], catalog: dict | None = None) -> dict[str, dict]:
    """Count only valid, linked instances; never infer ownership by guild majority."""
    if any(not isinstance(value_at(world, field, 'value'), list) for field in ('CharacterSaveParameterMap', 'CharacterContainerSaveData')):
        return {uid: unknown('world_field_missing') for uid in player_data}
    if catalog is None:
        catalog = json.loads(Path(__file__).with_name('character_ids.json').read_bytes())
    pals = set(catalog['pals'])
    npcs = set(catalog['npcs'])
    result = {uid: {'state': 'known', 'value': 0, 'ids': []} for uid in player_data}
    private = {}
    for uid, data in player_data.items():
        for field in ('PalStorageContainerId', 'OtomoCharacterContainerId'):
            cid = strict_guid(value_at(data, field, 'value', 'ID', 'value'))
            if not cid:
                result[uid] = unknown('player_container_missing')
            elif cid in private and private[cid] != uid:
                result[uid] = unknown('container_owner_conflict')
                result[private[cid]] = unknown('container_owner_conflict')
            else:
                private[cid] = uid
    containers, membership = character_containers(world)
    for cid, uid in private.items():
        if cid not in containers:
            result[uid] = unknown('player_container_missing')
    base_groups = {}
    for base in value_at(world, 'BaseCampSaveData', 'value') or []:
        cid = strict_guid(value_at(base, 'value', 'WorkerDirector', 'value', 'RawData', 'value', 'container_id'))
        gid = strict_guid(value_at(base, 'value', 'RawData', 'value', 'group_id_belong_to'))
        if cid and gid:
            base_groups.setdefault(cid, set()).add(gid)
    guilds = {}
    for group in value_at(world, 'GroupSaveDataMap', 'value') or []:
        gid = strict_guid(group.get('key'))
        for member in value_at(group, 'value', 'RawData', 'value', 'players') or []:
            guilds.setdefault(strict_guid(member.get('player_uid')), set()).add(gid)
    for uid, gids in guilds.items():
        if uid in result and (len(gids) != 1 or '' in gids):
            result[uid] = unknown('guild_membership_unverified')
    instances, counts = {}, {uid: set() for uid in player_data}
    for entry in value_at(world, 'CharacterSaveParameterMap', 'value') or []:
        sp = value_at(entry, 'value', 'RawData', 'value', 'object', 'SaveParameter', 'value')
        iid = strict_guid(value_at(entry, 'key', 'InstanceId', 'value'))
        if not isinstance(sp, dict) or not sp:
            for linked_cid in membership.get(iid, set()):
                affected = {private[linked_cid]} if linked_cid in private else {uid for uid, gids in guilds.items() if gids & base_groups.get(linked_cid, set())}
                for uid in affected:
                    if uid in result:
                        result[uid] = unknown('instance_parameter_missing')
            continue
        if value_at(sp, 'IsPlayer', 'value'):
            continue
        owner = strict_guid(value_at(sp, 'OwnerPlayerUId', 'value'))
        cid = strict_guid(value_at(sp, 'SlotId', 'value', 'ContainerId', 'value', 'ID', 'value'))
        affected = {uid for uid in (owner, private.get(cid)) if uid in result}
        affected.update(private[linked_cid] for linked_cid in membership.get(iid, set()) if linked_cid in private)
        if not affected:
            continue
        def invalidate(reason):
            for uid in affected:
                result[uid] = unknown(reason)
        character = value_at(sp, 'CharacterID', 'value')
        key = character.casefold() if isinstance(character, str) else ''
        if key in npcs:
            continue
        if key not in pals:
            invalidate('character_kind_unknown')
            continue
        if not iid or cid not in containers or membership.get(iid) != {cid}:
            invalidate('instance_container_mismatch')
            continue
        if owner not in result or (cid in private and private[cid] != owner):
            invalidate('container_owner_conflict')
            continue
        if cid not in private and (len(guilds.get(owner, set())) != 1 or base_groups.get(cid) != guilds[owner]):
            invalidate('container_owner_unverified')
            continue
        identity = (owner, cid, key)
        if iid in instances and instances[iid] != identity:
            invalidate('duplicate_instance_conflict')
            prior_owner = instances[iid][0]
            if prior_owner in result:
                result[prior_owner] = unknown('duplicate_instance_conflict')
            continue
        instances[iid] = identity
        counts[owner].add(iid)
    # Every occupied private slot must resolve to a character in the level.
    character_ids = {strict_guid(value_at(e, 'key', 'InstanceId', 'value')) for e in value_at(world, 'CharacterSaveParameterMap', 'value') or [] if isinstance(value_at(e, 'value', 'RawData', 'value', 'object', 'SaveParameter', 'value'), dict) and value_at(e, 'value', 'RawData', 'value', 'object', 'SaveParameter', 'value')}
    for iid, cids in membership.items():
        if iid not in character_ids:
            for cid in cids:
                if cid in private:
                    result[private[cid]] = unknown('instance_missing')
    for uid, metric in result.items():
        if metric['state'] == 'known':
            metric.update(value=len(counts[uid]), ids=sorted(counts[uid]))
    return result


def unattributed_metrics(world: dict, player_uids: list[str], catalog: dict | None = None) -> dict[str, dict]:
    """Shared base pals with no individual owner; informational, never a delta."""
    if any(not isinstance(value_at(world, field, 'value'), list) for field in ('CharacterSaveParameterMap', 'CharacterContainerSaveData', 'BaseCampSaveData', 'GroupSaveDataMap')):
        return {uid: unknown('shared_world_field_missing') for uid in player_uids}
    if catalog is None:
        catalog = json.loads(Path(__file__).with_name('character_ids.json').read_bytes())
    pals, npcs = set(catalog['pals']), set(catalog['npcs'])
    guild_members, memberships = {}, {}
    for group in value_at(world, 'GroupSaveDataMap', 'value') or []:
        gid = strict_guid(group.get('key'))
        for member in value_at(group, 'value', 'RawData', 'value', 'players') or []:
            uid = strict_guid(member.get('player_uid'))
            guild_members.setdefault(gid, set()).add(uid)
            memberships.setdefault(uid, set()).add(gid)
    guild_counts = {gid: set() for gid in guild_members}
    invalid = set()
    base_containers = {}
    for base in value_at(world, 'BaseCampSaveData', 'value') or []:
        gid = strict_guid(value_at(base, 'value', 'RawData', 'value', 'group_id_belong_to'))
        cid = strict_guid(value_at(base, 'value', 'WorkerDirector', 'value', 'RawData', 'value', 'container_id'))
        if not cid or (cid in base_containers and base_containers[cid] != gid):
            invalid.update({gid, base_containers.get(cid)})
        else:
            base_containers[cid] = gid
    containers, slots = character_containers(world)
    for cid, gid in base_containers.items():
        if cid not in containers:
            invalid.add(gid)
    seen, character_ids = {}, set()
    for character in value_at(world, 'CharacterSaveParameterMap', 'value') or []:
        iid = strict_guid(value_at(character, 'key', 'InstanceId', 'value'))
        sp = value_at(character, 'value', 'RawData', 'value', 'object', 'SaveParameter', 'value')
        if not isinstance(sp, dict) or not sp:
            invalid.update(base_containers[cid] for cid in slots.get(iid, set()) if cid in base_containers)
            continue
        character_ids.add(iid)
        cid = strict_guid(value_at(sp, 'SlotId', 'value', 'ContainerId', 'value', 'ID', 'value'))
        linked_cids = slots.get(iid, set())
        if cid not in linked_cids:
            invalid.update(base_containers[linked_cid] for linked_cid in linked_cids if linked_cid in base_containers)
        gid = base_containers.get(cid)
        if gid not in guild_counts or value_at(sp, 'IsPlayer', 'value'):
            continue
        if not iid or slots.get(iid) != {cid}:
            invalid.add(gid)
            continue
        owner = value_at(sp, 'OwnerPlayerUId', 'value')
        kind = value_at(sp, 'CharacterID', 'value')
        key = kind.casefold() if isinstance(kind, str) else ''
        identity = (cid, strict_guid(owner), key)
        if iid in seen and seen[iid] != identity:
            invalid.add(gid)
        seen[iid] = identity
        if strict_guid(owner) or key in npcs:
            continue
        if owner is not None and str(owner).replace('-', '') != '0' * 32:
            invalid.add(gid)
        elif key not in pals:
            invalid.add(gid)
        else:
            guild_counts[gid].add(iid)
    for iid, cids in slots.items():
        if iid not in character_ids:
            invalid.update(base_containers[cid] for cid in cids if cid in base_containers)
    result = {}
    for uid in player_uids:
        gids = memberships.get(uid, set())
        if len(gids) != 1 or '' in gids or not isinstance(value_at(world, 'BaseCampSaveData', 'value'), list):
            result[uid] = unknown('guild_membership_unverified')
            continue
        gid = next(iter(gids))
        result[uid] = unknown('shared_container_unverified') if gid in invalid else {'state': 'known', 'value': len(guild_counts[gid])}
    return result


def value_at(value: Any, *path: str) -> Any:
    current = value
    for key in path:
        if not isinstance(current, dict) or key not in current:
            return None
        current = current[key]
    return current


def uid_to_hex(uid: Any) -> str:
    if uid is None:
        return ""
    text = str(uid).replace("-", "").upper()
    if len(text) < 8:
        return ""
    return text[:32].ljust(32, "0")


def hex_to_decimal_uid(uid_hex: str) -> str:
    if not uid_hex:
        return ""
    return str(int(uid_hex[:8], 16))


def byte_value(prop: Any, default: int = 0) -> int:
    if not prop:
        return default
    value = prop.get("value") if isinstance(prop, dict) else prop
    if isinstance(value, dict):
        value = value.get("value", default)
    return int(value)


def fixed_point(prop: Any) -> int:
    if not prop:
        return 0
    return int(value_at(prop, "value", "Value", "value") or 0)


def tick_to_utc(tick: int, real_ticks: int, file_mtime: float) -> str:
    timestamp = file_mtime + (int(tick) - int(real_ticks)) / 10_000_000
    return utc_from_timestamp(timestamp)


def utc_from_timestamp(timestamp: float) -> str:
    return dt.datetime.fromtimestamp(timestamp, tz=dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


if __name__ == "__main__":
    raise SystemExit(main())
