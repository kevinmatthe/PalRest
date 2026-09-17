# PalRest Save Worker

`palrest-save-worker` extracts a compact, read-only world snapshot from a
Palworld `Level.sav` plus the sibling `Players/` directory.

The worker intentionally runs outside the Go service. It depends on
PalworldSaveTools' `palsav` and `palooz` packages at runtime, then emits
PalRest-owned JSON:

```bash
palrest-save-worker --level /data/pal-saves/Level.sav --output /tmp/world.json
```

For local development, install the parser dependencies into a venv and invoke
the script with that interpreter:

```bash
python3 -m venv /tmp/palrest-pst-venv
/tmp/palrest-pst-venv/bin/pip install \
  /tmp/PalworldSaveTools/src/palsav/palooz \
  /tmp/PalworldSaveTools/src/palsav \
  orjson
/tmp/palrest-pst-venv/bin/python tools/save_worker/palrest_save_worker.py \
  --level pal-saves/Level.sav \
  --output /tmp/palrest-world.json
```

The Dockerfile builds the same runtime in an isolated stage from the
PalworldSaveTools submodule revision pinned by the repository gitlink. If the
build environment needs a proxy, pass standard Docker build args or environment
variables.

`palsav` and `palooz` are GPL-licensed components. Keep parser use isolated from
the Go binary and treat image distribution accordingly.

Parser version 2 keeps `palrest.save_snapshot.v1` and adds player progress:

```json
{"progress":{"schema_version":1,"metrics":{
  "owned_pals":{"state":"known","value":2,"ids":["INSTANCE_GUID_1","INSTANCE_GUID_2"]},
  "capture_total":{"state":"known","value":12},
  "paldeck":{"state":"unknown","reason":"field_missing"},
  "fast_travel":{"state":"unsupported","reason":"field_shape_unsupported"}
}}}
```

`progress.unattributed_pals` is a separate `{state, value? , reason?}` metric
showing the count of valid, deduplicated base pals without OwnerPlayerUId in
the player's own guild. It is a shared count, never allocated to individuals
and never included in individual changes. Broken container or membership
references make this informational count unknown. The inspected world contains
228 ownerless worker records across 18 bases; this raw count includes records
whose container references did not validate, so it is not emitted as a known
shared count. Only guilds with complete verified references receive a number.

Known sets contain sorted unique IDs and a matching count. Missing values and
failed player parsing produce `unknown`; unrecognized field representations
produce `unsupported`. Neither includes a numeric value. Reasons are fixed
codes, never raw parser exceptions or file contents.

`source.world_id` comes from an explicit `--world-id ORIGINAL_WORLD_GUID`, or the
nearest ancestor directory with a valid world GUID. `world_id_kind` is
`explicit`, `directory`, or `unknown`. Level and LevelMeta in the validated
server save do not contain a world GUID. A relocated backup such as `bk0823`
therefore needs the explicit original world ID to participate in comparisons.
Names and guild membership never establish world/player identity.

`source.source_time` is the original Level.sav modification time in UTC and
`source_time_kind` is `file_mtime`. The root Unreal `Timestamp` was verified to
be a **server-local wall clock** (eight hours ahead of the UTC file time in the
sample). It is used only as a raw save-generation marker, never mislabeled UTC.
Copies that fail to preserve mtime lose the original observation time; consumers
must still treat counter regressions as a rollback boundary.

The worker copies Level.sav, optional LevelMeta.sav, and all non-DPS player
saves to a temporary directory. Before/after manifests compare the full file
set plus device, inode, size, mtime and ctime. A changed source fails extraction.
All parsing and hashing use the stable copy. `source.consistent` is true only
when Level, LevelMeta and every player file have the same positive root
Timestamp and player file identities match their embedded PlayerUId. Missing
metadata/files, parse errors or mixed generations disable comparisons; an
individual metric with uncertain ownership remains unknown independently.

The fingerprint includes file contents (including metadata), parser/progress
versions, resolved world identity and its kind, and the classification catalog.
It excludes filesystem timestamps and capture time, so a repeated copy does not
create an observation just because it was copied later.

Validated metric sources and definitions:

- `capture_total`: sum of `SaveData.RecordData.PalCaptureCount`, a Name→Int map.
  This is the saved cumulative capture record, **including Human entries**;
  it is neither current ownership nor evidence of a specific capture action.
- `paldeck`: IDs with a literal true flag in `PaldeckUnlockFlag` (Name→Bool).
- `fast_travel`: GUID IDs with a literal true flag in
  `FastTravelPointUnlockFlag` (Name→Bool); no location-based inference.
- `owned_pals`: non-player CharacterSaveParameterMap instances classified as
  pals, deduplicated by InstanceId, with matching CharacterContainerSaveData
  slots and SaveParameter.SlotId. A private container must belong to the same
  player through PalStorageContainerId/OtomoCharacterContainerId. Base workers
  require OwnerPlayerUId plus the worker container's base/guild membership.
  Captured NPCs and lost records without a valid active container are excluded.
  Conflicts, unknown character types and missing references produce unknown,
  never a partial count or attribution to every guild member.

`character_ids.json` contains only case-folded asset IDs classified as pals or
NPCs, extracted from the local PalworldSaveTools catalog; its source revision is
embedded. NPC IDs take precedence over the broad PST pals array; generic
`Human` is also classified as NPC because its source description explicitly
identifies it as human rather than a pal. The generated groups do not overlap.
No save data, names, artwork or descriptions are included. Regenerate:

```bash
python3 tools/save_worker/update_character_ids.py PalworldSaveTools
python -m unittest discover -s tools/save_worker -p 'test_*.py'
```

The runtime must install `character_ids.json` beside the worker script.
`source.progress_definition` is the SHA-256 of the exact classification catalog
bytes used for the metrics and fingerprint. Updating the catalog changes this
definition and automatically interrupts comparison with earlier definitions,
so reclassification cannot appear as player activity. Other changes to metric
meaning also require a parser/progress version change before deployment.

Read-only validation on two adjacent 2026-08-23 server backups confirmed matching
save generations across Level, LevelMeta and all 11 Players files, and all four
metrics for all players. One anonymous player changed owned pals 9→11, saved
capture records 9→11, paldeck 6→8, and fast travel 5→6; the added instance/species/
travel sets matched those count increases. No real saves or identifying player
values are committed as fixtures.
