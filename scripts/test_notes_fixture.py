"""Hermetic CGI durability regressions; no real /data or host-wide sync."""
import os
from pathlib import Path
import select
import subprocess
import tempfile
import time
import unittest


class NotesFixtureTests(unittest.TestCase):
    def test_kill_after_successful_ack_does_not_block_the_next_write(self):
        with tempfile.TemporaryDirectory(prefix='cairn-notes-post-ack-') as directory:
            root = Path(directory)
            source = (Path(__file__).resolve().parents[1] / 'examples/notes/cgi-bin/notes').read_text()
            source = source.replace('/data', directory)
            normal = root / 'normal'
            normal.write_text(source)
            paused = root / 'paused'
            paused.write_text(source + '\nIFS= read -r ignored < "$AFTER_ACK_FIFO"\n')
            fifo = root / 'after-ack'
            os.mkfifo(fifo)
            environment = dict(os.environ, REQUEST_METHOD='POST', CONTENT_LENGTH='5', AFTER_ACK_FIFO=str(fifo))
            process = subprocess.Popen(['/bin/sh', str(paused)], stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment)
            try:
                process.stdin.write(b'first'); process.stdin.close()
                self.assertTrue(select.select([process.stdout], [], [], 5)[0], 'no bounded HTTP response')
                self.assertEqual(process.stdout.readline(), b'Status: 201 Created\r\n')
                self.assertFalse((root / 'write.lock').exists(), 'success ACK escaped before lock removal')
                process.kill(); self.assertEqual(process.wait(timeout=5), -9)
                result = subprocess.run(['/bin/sh', str(normal)], input=b'next!', env=environment,
                                        capture_output=True, timeout=5, check=True)
                self.assertIn(b'Status: 201 Created', result.stdout)
                self.assertEqual((root / 'notes.txt').read_bytes(), b'first\nnext!\n')
                self.assertFalse((root / 'write.lock').exists())
            finally:
                if process.poll() is None:
                    process.kill(); process.wait(timeout=5)
                process.stdout.close(); process.stderr.close()

    def run_post(self, fail_sync=False, fail_lock_sync=False, fail_release=False, ambiguous_handoff=False):
        with tempfile.TemporaryDirectory(prefix="cairn-notes-cgi-") as directory:
            root = Path(directory)
            source = (Path(__file__).resolve().parents[1] / "examples/notes/cgi-bin/notes").read_text()
            script = root / "notes"
            # Remap the sample's fixed container mount to a private test dir.
            script.write_text(source.replace("/data", directory))
            sync = root / "sync"
            sync.write_text("#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" >> \"$SYNC_CALL\"\n"
                            "if [ \"$#\" -eq 1 ]; then\n"
                            "  [ \"$1\" = \"$EXPECTED_DIR\" ] || exit 78\n"
                            "  [ \"$FAIL_LOCK_SYNC\" = 0 ] || exit 79\n"
                            "else\n"
                            "  [ \"$#\" -eq 2 ] || exit 77\n"
                            "  [ \"$1\" = \"$EXPECTED_FILE\" ] || exit 78\n"
                            "  [ \"$2\" = \"$EXPECTED_DIR\" ] || exit 78\n"
                            "fi\n"
                            "[ \"$FAIL_SYNC\" = 0 ] || exit 79\n")
            sync.chmod(0o755)
            release = root / "rmdir"
            release.write_text('#!/bin/sh\n[ "$FAIL_RELEASE" = 0 ] || exit 80\n'
                               'if [ "$AMBIGUOUS_HANDOFF" = 1 ] && [ ! -f "$HANDOFF_DONE" ]; then\n'
                               '  /bin/rmdir "$@" || exit 81\n'
                               '  mkdir "$1" || exit 82\n'
                               '  touch "$HANDOFF_DONE"\n'
                               '  exit 83\n'
                               'fi\nexec /bin/rmdir "$@"\n')
            release.chmod(0o755)
            environment = dict(os.environ, REQUEST_METHOD="POST", CONTENT_LENGTH="5",
                               PATH=directory + os.pathsep + os.environ["PATH"],
                               SYNC_CALL=str(root / "sync-call"), EXPECTED_FILE=str(root / "notes.txt"), EXPECTED_DIR=directory,
                               FAIL_SYNC="1" if fail_sync else "0",
                               FAIL_LOCK_SYNC="1" if fail_lock_sync else "0",
                               FAIL_RELEASE="1" if fail_release else "0",
                               AMBIGUOUS_HANDOFF="1" if ambiguous_handoff else "0",
                               HANDOFF_DONE=str(root / 'handoff-done'))
            result = subprocess.run(["/bin/sh", str(script)], input=b"first", env=environment,
                                    capture_output=True, timeout=5, check=True)
            return result.stdout, (root / "sync-call").read_text().splitlines(), (root / "notes.txt").read_bytes(), (root / "write.lock").exists()

    def test_post_syncs_only_the_journal_before_acknowledging(self):
        response, sync_call, data, locked = self.run_post()
        self.assertEqual(sync_call[0], "2", "flush the journal and its directory, not unrelated filesystems")
        self.assertTrue(sync_call[1].endswith("/notes.txt"))
        self.assertEqual(sync_call[2], str(Path(sync_call[1]).parent))
        self.assertEqual(sync_call[3:], ["1", sync_call[2]], "persist lock-directory removal before durable ACK")
        self.assertIn(b"Status: 201 Created", response)
        self.assertEqual(data, b"first\n")
        self.assertFalse(locked)

    def test_lock_removal_sync_failure_is_not_acknowledged(self):
        response, _, data, locked = self.run_post(fail_lock_sync=True)
        self.assertIn(b"Status: 500", response)
        self.assertNotIn(b"Saved", response)
        self.assertEqual(data, b"first\n")
        self.assertFalse(locked)

    def test_lock_release_failure_is_not_acknowledged(self):
        response, _, data, locked = self.run_post(fail_release=True)
        self.assertIn(b"Status: 500", response)
        self.assertNotIn(b"Saved", response)
        self.assertEqual(data, b"first\n")
        self.assertTrue(locked, "a failed release must not claim successful cleanup")

    def test_uncertain_release_does_not_remove_a_successor_lock(self):
        response, _, data, locked = self.run_post(ambiguous_handoff=True)
        self.assertIn(b"Status: 500", response)
        self.assertNotIn(b"Saved", response)
        self.assertEqual(data, b"first\n")
        self.assertTrue(locked, "EXIT cleanup must not remove a successor's lock after uncertain release")

    def test_success_cannot_escape_before_lock_release(self):
        with tempfile.TemporaryDirectory(prefix="cairn-notes-ack-") as directory:
            root = Path(directory)
            source = (Path(__file__).resolve().parents[1] / "examples/notes/cgi-bin/notes").read_text()
            script = root / "notes"
            script.write_text(source.replace("/data", directory))
            for name, body in {
                "sync": '#!/bin/sh\nexit 0\n',
                "rmdir": '#!/bin/sh\nprintf ready > "$ENTERED"\nwhile [ ! -f "$RELEASE" ]; do sleep .01; done\nexec /bin/rmdir "$@"\n',
            }.items():
                (root / name).write_text(body)
                (root / name).chmod(0o755)
            environment = dict(os.environ, REQUEST_METHOD="POST", CONTENT_LENGTH="5",
                               PATH=directory + os.pathsep + os.environ["PATH"],
                               ENTERED=str(root / "entered"), RELEASE=str(root / "release"))
            process = subprocess.Popen(["/bin/sh", str(script)], stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment)
            try:
                process.stdin.write(b"first"); process.stdin.close()
                deadline = time.monotonic() + 5
                while not (root / "entered").exists():
                    self.assertLess(time.monotonic(), deadline, "release hook was not reached")
                    self.assertIsNone(process.poll())
                    time.sleep(.01)
                readable, _, _ = select.select([process.stdout], [], [], .1)
                early = os.read(process.stdout.fileno(), 65536) if readable else b""
                self.assertNotIn(b"201 Created", early, "acknowledging before release can strand a durable lock after crash")
                (root / "release").touch()
                self.assertEqual(process.wait(timeout=5), 0)
                self.assertIn(b"Status: 201 Created", early + process.stdout.read())
                self.assertFalse((root / "write.lock").exists())
            finally:
                (root / "release").touch()
                if process.poll() is None:
                    process.kill(); process.wait(timeout=5)
                process.stdout.close(); process.stderr.close()

    def test_sync_failure_is_not_acknowledged_as_a_durable_write(self):
        response, _, data, locked = self.run_post(fail_sync=True)
        self.assertIn(b"Status: 500", response)
        self.assertNotIn(b"Saved", response)
        self.assertEqual(data, b"first\n")  # failed writes remain indeterminate
        self.assertFalse(locked)


if __name__ == "__main__":
    unittest.main()
