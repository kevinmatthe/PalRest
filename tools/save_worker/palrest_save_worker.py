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
from pathlib import Path
from typing import Any

from palsav.core import decompress_sav_to_gvas
from palsav.gvas import GvasFile
from palsav.paltypes import PALWORLD_CUSTOM_PROPERTIES, PALWORLD_TYPE_HINTS


PARSER_NAME = "palrest-palsav-worker"
PARSER_VERSION = 1


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
    parser.add_argument("--output", "-o", help="Output JSON path. Defaults to stdout.")
    parser.add_argument("--pretty", action="store_true", help="Pretty-print JSON output")
    args = parser.parse_args()

    level_path = Path(args.level)
    snapshot = extract_snapshot(level_path)
    encoded = json.dumps(snapshot, ensure_ascii=False, indent=2 if args.pretty else None, separators=None if args.pretty else (",", ":"))
    if args.output:
        Path(args.output).write_text(encoded + "\n", encoding="utf-8")
    else:
        print(encoded)
    return 0


def extract_snapshot(level_path: Path) -> dict[str, Any]:
    if level_path.name != "Level.sav":
        raise ValueError(f"{level_path} is not Level.sav")
    if not level_path.is_file():
        raise FileNotFoundError(level_path)
    players_dir = level_path.parent / "Players"
    if not players_dir.is_dir():
        raise FileNotFoundError(players_dir)

    level_stat = level_path.stat()
    with gc_paused():
        level_gvas = read_gvas(level_path)
    world = level_gvas.properties["worldSaveData"]["value"]
    real_ticks = value_at(world, "GameTimeSaveData", "value", "RealDateTimeTicks", "value") or 0
    fingerprint = snapshot_fingerprint(level_path, players_dir)

    players = extract_players(world, real_ticks, level_stat.st_mtime)
    guilds = extract_guilds(world, real_ticks, level_stat.st_mtime)

    return {
        "schema": "palrest.save_snapshot.v1",
        "parser": {"name": PARSER_NAME, "version": PARSER_VERSION},
        "source": {
            "level_sav": str(level_path),
            "fingerprint": fingerprint,
            "level_sav_size": level_stat.st_size,
            "level_sav_mtime": utc_from_timestamp(level_stat.st_mtime),
            "captured_at": utc_now(),
            "player_file_count": len([p for p in players_dir.glob("*.sav") if not p.name.endswith("_dps.sav")]),
        },
        "players": players,
        "guilds": guilds,
    }


def read_gvas(path: Path) -> GvasFile:
    raw_gvas, _ = decompress_sav_to_gvas(path.read_bytes())
    return GvasFile.read(raw_gvas, PALWORLD_TYPE_HINTS, PALWORLD_CUSTOM_PROPERTIES)


def snapshot_fingerprint(level_path: Path, players_dir: Path) -> str:
    digest = hashlib.sha256()
    paths = [level_path, *sorted(p for p in players_dir.glob("*.sav") if not p.name.endswith("_dps.sav"))]
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
