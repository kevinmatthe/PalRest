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

The Dockerfile builds the same runtime in an isolated stage pinned by
`PALWORLD_SAVE_TOOLS_REF`. If the build environment needs a proxy, pass it as
standard Docker build args or environment variables.

`palsav` and `palooz` are GPL-licensed components. Keep parser use isolated from
the Go binary and treat image distribution accordingly.
