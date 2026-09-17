"""Rebuild the classification-only catalog from a local PST checkout."""
import argparse
import json
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('pst_root', type=Path)
    args = parser.parse_args()
    source = args.pst_root / 'resources/game_data/characters.json'
    data = json.loads(source.read_text())
    # PST's pals array is a broad character catalog and includes every NPC.
    # Its Human asset explicitly says 'Humans are not Pals' in description,
    # but that generic asset is absent from its separate npcs array.
    npcs = {entry['asset'].casefold() for entry in data['npcs']} | {'human'}
    pals = {entry['asset'].casefold() for entry in data['pals']} - npcs
    catalog = {
        'source': 'PalworldSaveTools/resources/game_data/characters.json',
        'source_revision': subprocess.check_output(['git', '-C', str(args.pst_root), 'rev-parse', 'HEAD'], text=True).strip(),
        'pals': sorted(pals),
        'npcs': sorted(npcs),
    }
    Path(__file__).with_name('character_ids.json').write_text(json.dumps(catalog, indent=2) + '\n')


if __name__ == '__main__':
    main()
