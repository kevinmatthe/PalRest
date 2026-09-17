"""Synthetic-only regression fixtures: no game saves or player data."""
import contextlib
import hashlib
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import palrest_save_worker as worker


def prop(value):
    return {'value': value}


def record(field, entries, value_type):
    return {'RecordData': prop({field: {'type': 'MapProperty', 'key_type': 'NameProperty', 'value_type': value_type, 'value': entries}})}


class ProgressTests(unittest.TestCase):
    def test_growth_raw_values_preserve_zero_experience_and_missing(self):
        sp = {'IsPlayer': prop(True), 'Level': {'type': 'ByteProperty', 'value': {'value': 7}}, 'Exp': {'type': 'IntProperty', 'value': 0}}
        def extract():
            world = {'CharacterSaveParameterMap': prop([{'key': {'PlayerUId': prop('1' * 32)}, 'value': {'RawData': prop({'object': {'SaveParameter': prop(sp)}})}}])}
            player = worker.extract_players(world, 0, 0)[0]
            self.assertIn('progress', player)
            return player['progress']['metrics']
        self.assertEqual(extract()['level'], {'state': 'known', 'value': 7})
        self.assertEqual(extract()['experience'], {'state': 'known', 'value': 0})
        for field, name in [('Level', 'level'), ('Exp', 'experience')]:
            previous = sp.pop(field)
            self.assertEqual(extract()[name], {'state': 'unknown', 'reason': 'field_missing'})
            for invalid in [None, True, -1, 1.5, '12', {}, 9007199254740992] + ([0] if field == 'Level' else []):
                with self.subTest(field=field, invalid=invalid):
                    sp[field] = prop(invalid)
                    metric = extract()[name]
                    self.assertEqual(metric['state'], 'unknown')
                    self.assertNotIn('value', metric)
            sp[field] = previous

    def test_missing_is_unknown_not_zero(self):
        result = worker.record_metric({}, 'PalCaptureCount')
        self.assertEqual(result['state'], 'unknown')
        self.assertNotIn('value', result)

    def test_capture_counts_are_summed_and_duplicate_keys_rejected(self):
        result = worker.record_metric(record('PalCaptureCount', [{'key': 'Sheep', 'value': 12}, {'key': 'Human', 'value': 2}], 'IntProperty'), 'PalCaptureCount')
        self.assertEqual(result, {'state': 'known', 'value': 14})
        result = worker.record_metric(record('PalCaptureCount', [{'key': 'Sheep', 'value': 12}, {'key': 'Sheep', 'value': 12}], 'IntProperty'), 'PalCaptureCount')
        self.assertEqual(result['state'], 'unknown')

    def test_conflicting_fast_travel_guid_aliases_are_unknown(self):
        entries = [{'key': 'A' * 32, 'value': True}, {'key': 'AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA', 'value': False}]
        self.assertEqual(worker.record_metric(record('FastTravelPointUnlockFlag', entries, 'BoolProperty'), 'FastTravelPointUnlockFlag')['state'], 'unknown')

    def test_false_unlock_flags_do_not_count(self):
        result = worker.record_metric(record('PaldeckUnlockFlag', [{'key': 'B', 'value': True}, {'key': 'A', 'value': False}], 'BoolProperty'), 'PaldeckUnlockFlag')
        self.assertEqual(result, {'state': 'known', 'value': 1, 'ids': ['B']})

    def test_unsupported_shape_and_invalid_counts_do_not_become_zero(self):
        result = worker.record_metric(record('PalCaptureCount', [], 'StrProperty'), 'PalCaptureCount')
        self.assertEqual(result['state'], 'unsupported')
        for invalid in [-1, True, '12']:
            result = worker.record_metric(record('PalCaptureCount', [{'key': 'A', 'value': invalid}], 'IntProperty'), 'PalCaptureCount')
            self.assertEqual(result['state'], 'unknown')

    def test_timestamp_and_world_identity(self):
        self.assertEqual(worker.save_generation({'Timestamp': prop(621355968000000000)}), 621355968000000000)
        self.assertIsNone(worker.save_generation({'Timestamp': prop(True)}))
        guid = 'A' * 32
        self.assertEqual(worker.world_identity(Path('/saves') / guid / 'backup/world/date/Level.sav', None), (guid, 'directory'))
        self.assertEqual(worker.world_identity(Path('/saves/bk/Level.sav'), None), ('', 'unknown'))
        with self.assertRaises(ValueError):
            worker.world_identity(Path('/saves/bk/Level.sav'), 'bad')

    def test_stable_copy_keeps_original_bytes_and_hashes_meta(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'Players').mkdir()
            for name in ['Level.sav', 'LevelMeta.sav', 'Players/a.sav']:
                (root / name).write_bytes(name.encode())
            with worker.stable_snapshot(root / 'Level.sav') as (level, manifest):
                fingerprint = worker.snapshot_fingerprint(level, level.parent / 'Players')
                with patch.object(worker, 'PARSER_VERSION', 2):
                    self.assertNotEqual(worker.snapshot_fingerprint(level, level.parent / 'Players'), fingerprint)
                self.assertNotEqual(worker.snapshot_fingerprint(level, level.parent / 'Players', 'A' * 32, 'explicit'), fingerprint)
                (root / 'Level.sav').write_bytes(b'changed after copying')
                self.assertEqual(level.read_bytes(), b'Level.sav')
                (level.parent / 'LevelMeta.sav').write_bytes(b'different generation')
                self.assertNotEqual(worker.snapshot_fingerprint(level, level.parent / 'Players'), fingerprint)

    def test_copy_rejects_source_file_set_change(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'Players').mkdir()
            (root / 'Level.sav').write_bytes(b'world')
            original = worker.shutil.copy2
            def mutate(source, destination):
                result = original(source, destination)
                (root / 'Players/new.sav').write_bytes(b'new player')
                return result
            with patch.object(worker.shutil, 'copy2', side_effect=mutate):
                with self.assertRaises(worker.UnstableSnapshotError):
                    with worker.stable_snapshot(root / 'Level.sav'):
                        pass


class OwnershipTests(unittest.TestCase):
    uid, other, cid, iid = '1' * 32, '2' * 32, '3' * 32, '4' * 32

    def fixture(self):
        sp = {'CharacterID': prop('Alpaca'), 'OwnerPlayerUId': prop(self.uid), 'SlotId': prop({'ContainerId': prop({'ID': prop(self.cid)})})}
        character = {'key': {'InstanceId': prop(self.iid)}, 'value': {'RawData': prop({'object': {'SaveParameter': prop(sp)}})}}
        slot = {'RawData': prop({'instance_id': self.iid})}
        container = {'key': {'ID': prop(self.cid)}, 'value': {'Slots': prop({'values': [slot]})}}
        world = {'CharacterSaveParameterMap': prop([character]), 'CharacterContainerSaveData': prop([container])}
        data = {self.uid: {'PalStorageContainerId': prop({'ID': prop(self.cid)}), 'OtomoCharacterContainerId': prop({'ID': prop(self.cid)})}}
        return world, data, sp

    def test_missing_world_maps_are_unknown(self):
        world, data, sp = self.fixture()
        del world['CharacterSaveParameterMap']
        world['CharacterContainerSaveData']['value'][0]['value']['Slots']['value']['values'] = []
        self.assertEqual(worker.owned_metrics(world, data)[self.uid]['state'], 'unknown')

    def test_missing_slots_and_unparsed_occupied_characters_are_unknown(self):
        for mutation in ('missing_slots', 'missing_save_parameter', 'incomplete_save_parameter'):
            with self.subTest(mutation=mutation):
                world, data, sp = self.fixture()
                if mutation == 'missing_slots':
                    world['CharacterSaveParameterMap']['value'] = []
                    del world['CharacterContainerSaveData']['value'][0]['value']['Slots']
                elif mutation == 'missing_save_parameter':
                    world['CharacterSaveParameterMap']['value'][0]['value']['RawData']['value']['object'] = {}
                else:
                    world['CharacterSaveParameterMap']['value'][0]['value']['RawData']['value']['object']['SaveParameter'] = prop({'CharacterID': prop('Alpaca')})
                metric = worker.owned_metrics(world, data)[self.uid]
                self.assertEqual(metric['state'], 'unknown')
                self.assertNotIn('value', metric)

    def test_conflicting_guild_membership_is_unknown_in_either_order(self):
        world, data, sp = self.fixture()
        first, second = '5' * 32, '6' * 32
        world['GroupSaveDataMap'] = prop([{'key': gid, 'value': {'RawData': prop({'players': [{'player_uid': self.uid}]})}} for gid in (first, second)])
        world['BaseCampSaveData'] = prop([{'value': {'RawData': prop({'group_id_belong_to': second}), 'WorkerDirector': prop({'RawData': prop({'container_id': self.cid})})}}])
        private = '7' * 32
        data[self.uid] = {field: prop({'ID': prop(private)}) for field in ('PalStorageContainerId', 'OtomoCharacterContainerId')}
        world['CharacterContainerSaveData']['value'].append({'key': {'ID': prop(private)}, 'value': {'Slots': prop({'values': []})}})
        results = []
        for _ in range(2):
            results.append(worker.owned_metrics(world, data)[self.uid])
            world['GroupSaveDataMap']['value'].reverse()
        self.assertEqual(results[0]['state'], 'unknown')
        self.assertEqual(results[0], results[1])

    def test_bad_base_slot_payload_invalidates_individual_ownership(self):
        world, data, sp = self.fixture()
        guild, private = '5' * 32, '7' * 32
        data[self.uid] = {field: prop({'ID': prop(private)}) for field in ('PalStorageContainerId', 'OtomoCharacterContainerId')}
        world['CharacterContainerSaveData']['value'].append({'key': {'ID': prop(private)}, 'value': {'Slots': prop({'values': []})}})
        world['CharacterContainerSaveData']['value'][0]['value']['Slots']['value']['values'].append({})
        world['GroupSaveDataMap'] = prop([{'key': guild, 'value': {'RawData': prop({'players': [{'player_uid': self.uid}]})}}])
        world['BaseCampSaveData'] = prop([{'value': {'RawData': prop({'group_id_belong_to': guild}), 'WorkerDirector': prop({'RawData': prop({'container_id': self.cid})})}}])
        self.assertEqual(worker.owned_metrics(world, data)[self.uid]['state'], 'unknown')

    def test_conflicting_base_guilds_are_unknown_in_either_order(self):
        world, data, sp = self.fixture()
        first, second, private = '5' * 32, '6' * 32, '7' * 32
        data[self.uid] = {field: prop({'ID': prop(private)}) for field in ('PalStorageContainerId', 'OtomoCharacterContainerId')}
        world['CharacterContainerSaveData']['value'].append({'key': {'ID': prop(private)}, 'value': {'Slots': prop({'values': []})}})
        world['GroupSaveDataMap'] = prop([{'key': second, 'value': {'RawData': prop({'players': [{'player_uid': self.uid}]})}}])
        world['BaseCampSaveData'] = prop([{'value': {'RawData': prop({'group_id_belong_to': gid}), 'WorkerDirector': prop({'RawData': prop({'container_id': self.cid})})}} for gid in (first, second)])
        results = []
        for _ in range(2):
            results.append(worker.owned_metrics(world, data)[self.uid])
            world['BaseCampSaveData']['value'].reverse()
        self.assertEqual(results[0]['state'], 'unknown')
        self.assertEqual(results[0], results[1])

    def test_counts_unique_valid_instances(self):
        world, data, sp = self.fixture()
        world['CharacterSaveParameterMap']['value'] *= 2
        self.assertEqual(worker.owned_metrics(world, data)[self.uid], {'state': 'known', 'value': 1, 'ids': [self.iid]})

    def test_does_not_count_unlinked_or_wrong_owner_instances(self):
        for mutation in ('unlinked', 'other_owner', 'unknown_species'):
            world, data, sp = self.fixture()
            if mutation == 'unlinked':
                world['CharacterContainerSaveData']['value'][0]['value']['Slots']['value']['values'] = []
            elif mutation == 'other_owner':
                sp['OwnerPlayerUId'] = prop(self.other)
            else:
                sp['CharacterID'] = prop('UnverifiedNewSpecies')
            result = worker.owned_metrics(world, data)[self.uid]
            self.assertEqual(result['state'], 'unknown', mutation)
            self.assertNotIn('value', result)

    def test_captured_human_is_not_a_pal(self):
        world, data, sp = self.fixture()
        for character in ('BOSS_Male_People03', 'Human'):
            with self.subTest(character=character):
                sp['CharacterID'] = prop(character)
                self.assertEqual(worker.owned_metrics(world, data)[self.uid]['value'], 0)

    def test_occupied_slot_with_missing_character_is_unknown(self):
        world, data, sp = self.fixture()
        world['CharacterSaveParameterMap']['value'] = []
        self.assertEqual(worker.owned_metrics(world, data)[self.uid]['state'], 'unknown')


class UnattributedTests(unittest.TestCase):
    uid, other, cid, iid = OwnershipTests.uid, OwnershipTests.other, OwnershipTests.cid, OwnershipTests.iid
    fixture = OwnershipTests.fixture
    def shared_fixture(self):
        world, data, sp = self.fixture()
        guild = '5' * 32
        sp.pop('OwnerPlayerUId')
        world['GroupSaveDataMap'] = prop([{'key': guild, 'value': {'RawData': prop({'players': [{'player_uid': self.uid}]})}}])
        world['BaseCampSaveData'] = prop([{'value': {'RawData': prop({'group_id_belong_to': guild}), 'WorkerDirector': prop({'RawData': prop({'container_id': self.cid})})}}])
        return world, sp

    def test_missing_slots_and_unparsed_shared_characters_are_unknown(self):
        for mutation in ('missing_slots', 'missing_save_parameter', 'incomplete_save_parameter'):
            with self.subTest(mutation=mutation):
                world, sp = self.shared_fixture()
                if mutation == 'missing_slots':
                    world['CharacterSaveParameterMap']['value'] = []
                    del world['CharacterContainerSaveData']['value'][0]['value']['Slots']
                elif mutation == 'missing_save_parameter':
                    world['CharacterSaveParameterMap']['value'][0]['value']['RawData']['value']['object'] = {}
                else:
                    world['CharacterSaveParameterMap']['value'][0]['value']['RawData']['value']['object']['SaveParameter'] = prop({'CharacterID': prop('Alpaca')})
                metric = worker.unattributed_metrics(world, [self.uid])[self.uid]
                self.assertEqual(metric['state'], 'unknown')
                self.assertNotIn('value', metric)

    def test_unrelated_broken_guild_does_not_invalidate_known_shared_count(self):
        world, sp = self.shared_fixture()
        other_guild = '7' * 32
        world['GroupSaveDataMap']['value'].append({'key': other_guild, 'value': {'RawData': prop({'players': [{'player_uid': self.other}]})}})
        world['BaseCampSaveData']['value'].append({'value': {'RawData': prop({'group_id_belong_to': other_guild})}})
        metrics = worker.unattributed_metrics(world, [self.uid, self.other])
        self.assertEqual(metrics[self.uid], {'state': 'known', 'value': 1})
        self.assertEqual(metrics[self.other]['state'], 'unknown')

    def test_shared_base_pals_are_counted_once_as_unattributed(self):
        world, sp = self.shared_fixture()
        world['CharacterSaveParameterMap']['value'] *= 2
        self.assertEqual(worker.unattributed_metrics(world, [self.uid])[self.uid], {'state': 'known', 'value': 1})
        sp['OwnerPlayerUId'] = prop(self.uid)
        self.assertEqual(worker.unattributed_metrics(world, [self.uid])[self.uid]['value'], 0)

    def test_other_guilds_are_not_attributed_and_broken_slots_are_unknown(self):
        world, sp = self.shared_fixture()
        self.assertEqual(worker.unattributed_metrics(world, [self.other])[self.other]['state'], 'unknown')
        world['CharacterContainerSaveData']['value'][0]['value']['Slots']['value']['values'] = []
        self.assertEqual(worker.unattributed_metrics(world, [self.uid])[self.uid]['state'], 'unknown')


class SnapshotTests(unittest.TestCase):
    def test_mixed_generation_is_not_comparable_and_parse_errors_are_safe(self):
        from types import SimpleNamespace
        uid = '1' * 32
        ticks = 639230480446090000
        sp = {'IsPlayer': prop(True), 'NickName': prop('Synthetic'), 'Level': prop({'value': 7}), 'Exp': prop(0)}
        level = {'Timestamp': prop(ticks), 'worldSaveData': prop({'CharacterSaveParameterMap': prop([{'key': {'PlayerUId': prop(uid)}, 'value': {'RawData': prop({'object': {'SaveParameter': prop(sp)}})}}])})}
        player = {'Timestamp': prop(ticks - 1), 'SaveData': prop({'PlayerUId': prop(uid)})}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'Players').mkdir()
            for name in ['Level.sav', 'LevelMeta.sav', f'Players/{uid}.sav']:
                (root / name).write_bytes(b'synthetic')
            def reader(path):
                values = {'Level.sav': level, 'LevelMeta.sav': {'Timestamp': prop(ticks)}, f'{uid}.sav': player}
                return SimpleNamespace(properties=values[path.name])
            with patch.object(worker, 'read_gvas', side_effect=reader):
                result = worker.extract_snapshot(root / 'Level.sav', 'A' * 32)
            self.assertEqual(result['parser']['version'], 3)
            self.assertEqual(result['players'][0]['progress']['metrics']['level'], {'state': 'known', 'value': 7})
            self.assertEqual(result['players'][0]['progress']['metrics']['experience'], {'state': 'known', 'value': 0})
            catalog_path = Path(worker.__file__).with_name('character_ids.json')
            catalog_bytes = catalog_path.read_bytes()
            self.assertEqual(result['source']['progress_definition'], hashlib.sha256(catalog_bytes).hexdigest())
            original_read = Path.read_bytes
            def changed_catalog(path):
                return catalog_bytes + b'\n' if path == catalog_path else original_read(path)
            with patch.object(worker, 'read_gvas', side_effect=reader), patch.object(Path, 'read_bytes', changed_catalog):
                changed = worker.extract_snapshot(root / 'Level.sav', 'A' * 32)
            self.assertNotEqual(changed['source']['progress_definition'], result['source']['progress_definition'])
            self.assertNotEqual(changed['source']['fingerprint'], result['source']['fingerprint'])
            self.assertFalse(result['source']['consistent'])
            self.assertEqual(result['source']['consistency_reason'], 'save_generation_mismatch')
            def broken_reader(path):
                if path.name == f'{uid}.sav':
                    raise ValueError('PRIVATE SECRET')
                return reader(path)
            with patch.object(worker, 'read_gvas', side_effect=broken_reader):
                result = worker.extract_snapshot(root / 'Level.sav', 'A' * 32)
            self.assertFalse(result['source']['consistent'])
            self.assertNotIn('PRIVATE SECRET', str(result))
            self.assertEqual(result['players'][0]['progress']['metrics']['capture_total'], {'state': 'unknown', 'reason': 'player_parse_failed'})
            # Growth is present in Level.sav even when the companion player file fails.
            self.assertEqual(result['players'][0]['progress']['metrics']['level'], {'state': 'known', 'value': 7})
            self.assertEqual(result['players'][0]['progress']['metrics']['experience'], {'state': 'known', 'value': 0})


if __name__ == '__main__':
    unittest.main()
