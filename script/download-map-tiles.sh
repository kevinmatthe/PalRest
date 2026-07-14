#!/usr/bin/env bash
#
# Download the Palworld map tile pyramid and upload it to this repo via Git LFS.
#
# Run this on a machine with good network. It downloads every tile the WebUI map
# expects (webui/public/map/tiles/{z}/{x}/{y}.png, zoom 0..6 = 5461 tiles, ~122MB),
# stores them as Git LFS objects, then commits and pushes to the current branch.
#
# The download is idempotent: existing, non-empty tiles are skipped, so a failed
# run can simply be re-invoked. Behind a proxy, export http_proxy/https_proxy first.
#
# Usage:
#   script/download-map-tiles.sh                # download + commit + push
#   NO_PUSH=1   script/download-map-tiles.sh    # download + commit, skip push
#   NO_COMMIT=1 script/download-map-tiles.sh    # download only
#   JOBS=8      script/download-map-tiles.sh    # tune parallelism (default 16)
#
set -euo pipefail

BASE_URL="https://palworld.gg/images/tiles"
MAX_ZOOM=6
JOBS="${JOBS:-16}"

repo_root="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
out_dir="$repo_root/webui/public/map/tiles"
err_log="$(mktemp)"
trap 'rm -f "$err_log"' EXIT

command -v git >/dev/null || { echo "git is required" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
git lfs version >/dev/null 2>&1 || { echo "git-lfs is required (https://git-lfs.com)" >&2; exit 1; }

echo "Repo:   $repo_root"
echo "Output: $out_dir"
echo "Zoom:   0..$MAX_ZOOM   Jobs: $JOBS"

git -C "$repo_root" lfs install --local >/dev/null
# Ensure the tiles are tracked by LFS even on a fresh clone that predates it.
if ! git -C "$repo_root" check-attr filter -- webui/public/map/tiles/0/0/0.png 2>/dev/null | grep -q 'filter: lfs'; then
  git -C "$repo_root" lfs track "webui/public/map/tiles/**/*.png" >/dev/null
fi

mkdir -p "$out_dir"

fetch_tile() {
  local z="$1" x="$2" y="$3"
  local dir="$out_dir/$z/$x"
  local file="$dir/$y.png"
  [ -s "$file" ] && return 0
  mkdir -p "$dir"
  local code
  code="$(curl -fsS --retry 4 --retry-delay 2 --connect-timeout 20 -o "$file" -w '%{http_code}' "$BASE_URL/$z/$x/$y.png" || true)"
  if [ "$code" != "200" ] || [ ! -s "$file" ]; then
    rm -f "$file"
    echo "FAIL z=$z x=$x y=$y http=$code" >>"$err_log"
    return 1
  fi
}
export -f fetch_tile
export BASE_URL out_dir err_log

echo "Downloading tiles…"
for z in $(seq 0 "$MAX_ZOOM"); do
  max=$(( (1 << z) - 1 ))
  for x in $(seq 0 "$max"); do
    for y in $(seq 0 "$max"); do
      printf '%s %s %s\n' "$z" "$x" "$y"
    done
  done
done | xargs -P "$JOBS" -n 3 bash -c 'fetch_tile "$@"' _ || true

if [ -s "$err_log" ]; then
  echo "ERROR: $(wc -l <"$err_log") tiles failed to download. Re-run to retry:" >&2
  head -10 "$err_log" >&2
  exit 1
fi

# Verify the full pyramid is present (sum of 4^z for z in 0..6 = 5461).
expected=0
for z in $(seq 0 "$MAX_ZOOM"); do expected=$(( expected + (1 << z) * (1 << z) )); done
actual="$(find "$out_dir" -name '*.png' | wc -l | tr -d ' ')"
echo "Tiles: $actual / $expected"
if [ "$actual" != "$expected" ]; then
  echo "ERROR: tile count mismatch; expected $expected, found $actual." >&2
  exit 1
fi

if [ -n "${NO_COMMIT:-}" ]; then
  echo "Download complete. NO_COMMIT set — skipping commit/push."
  exit 0
fi

git -C "$repo_root" add .gitattributes webui/public/map/tiles
if git -C "$repo_root" diff --cached --quiet; then
  echo "Nothing to commit — tiles already up to date."
  exit 0
fi

git -C "$repo_root" commit -m "Add Palworld map tiles (z=0..6) via Git LFS"

if [ -n "${NO_PUSH:-}" ]; then
  echo "Committed. NO_PUSH set — run 'git push' when ready."
  exit 0
fi

branch="$(git -C "$repo_root" rev-parse --abbrev-ref HEAD)"
echo "Pushing LFS objects and commit to origin/$branch…"
git -C "$repo_root" push origin "$branch"
echo "Done."
