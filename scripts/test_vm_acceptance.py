import unittest
from unittest.mock import patch
import vm_acceptance as vm


class SourceFreezeTests(unittest.TestCase):
    def identity(self, path):
        return {'commit': path.name * 3, 'working_tree': '', 'diff_sha256': 'unchanged'}

    def test_clean_sources_are_accepted_and_rechecked(self):
        with patch.object(vm, 'source_revision', side_effect=self.identity):
            inputs = vm.capture_source_inputs()
            self.assertEqual(set(inputs), {'SERVER', 'DURAFLOW', 'Mini-Docker'})
            vm.verify_source_inputs(inputs)

    def test_dirty_runtime_cannot_be_final_revision_proof(self):
        def dirty(path):
            item = self.identity(path)
            if path.name == 'Mini-Docker':
                item['working_tree'] = ' M mini_docker/filesystem.py\n'
            return item
        with patch.object(vm, 'source_revision', side_effect=dirty):
            with self.assertRaisesRegex(RuntimeError, 'requires clean source: Mini-Docker'):
                vm.capture_source_inputs()
            self.assertTrue(vm.capture_source_inputs(allow_dirty=True)['Mini-Docker']['working_tree'])

    def test_mid_build_commit_or_same_status_content_change_is_rejected(self):
        for field in ('commit', 'diff_sha256', 'working_tree'):
            with self.subTest(field=field), patch.object(vm, 'source_revision', side_effect=self.identity):
                inputs = vm.capture_source_inputs()
                inputs['SERVER'][field] = 'different before capture'
                with self.assertRaisesRegex(RuntimeError, 'source changed.*SERVER'):
                    vm.verify_source_inputs(inputs)


if __name__ == '__main__':
    unittest.main()
