"""Hermetic CGI durability regressions; no real /data or host-wide sync."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class NotesFixtureTests(unittest.TestCase):
    def run_post(self, fail_sync=False):
        with tempfile.TemporaryDirectory(prefix="cairn-notes-cgi-") as directory:
            root = Path(directory)
            source = (Path(__file__).resolve().parents[1] / "examples/notes/cgi-bin/notes").read_text()
            script = root / "notes"
            # Remap the sample's fixed container mount to a private test dir.
            script.write_text(source.replace("/data", directory))
            sync = root / "sync"
            sync.write_text("#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" > \"$SYNC_CALL\"\n"
                            "[ \"$#\" -eq 2 ] || exit 77\n"
                            "[ \"$1\" = \"$EXPECTED_FILE\" ] || exit 78\n"
                            "[ \"$2\" = \"$EXPECTED_DIR\" ] || exit 78\n"
                            "[ \"$FAIL_SYNC\" = 0 ] || exit 79\n")
            sync.chmod(0o755)
            environment = dict(os.environ, REQUEST_METHOD="POST", CONTENT_LENGTH="5",
                               PATH=directory + os.pathsep + os.environ["PATH"],
                               SYNC_CALL=str(root / "sync-call"), EXPECTED_FILE=str(root / "notes.txt"), EXPECTED_DIR=directory,
                               FAIL_SYNC="1" if fail_sync else "0")
            result = subprocess.run(["/bin/sh", str(script)], input=b"first", env=environment,
                                    capture_output=True, timeout=5, check=True)
            return result.stdout, (root / "sync-call").read_text().splitlines(), (root / "notes.txt").read_bytes(), (root / "write.lock").exists()

    def test_post_syncs_only_the_journal_before_acknowledging(self):
        response, sync_call, data, locked = self.run_post()
        self.assertEqual(sync_call[0], "2", "flush the journal and its directory, not unrelated filesystems")
        self.assertTrue(sync_call[1].endswith("/notes.txt"))
        self.assertEqual(sync_call[2], str(Path(sync_call[1]).parent))
        self.assertIn(b"Status: 201 Created", response)
        self.assertEqual(data, b"first\n")
        self.assertFalse(locked)

    def test_sync_failure_is_not_acknowledged_as_a_durable_write(self):
        response, _, data, locked = self.run_post(fail_sync=True)
        self.assertIn(b"Status: 500", response)
        self.assertNotIn(b"Saved", response)
        self.assertEqual(data, b"first\n")  # failed writes remain indeterminate
        self.assertFalse(locked)


if __name__ == "__main__":
    unittest.main()
