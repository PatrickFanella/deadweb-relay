"""Archive acceptance checks; no Docker daemon or production data required."""

import io
from pathlib import Path
import tarfile
import tempfile
import unittest

from pre_deploy_backup import inventory


class ArchiveTests(unittest.TestCase):
    def archive(self, entries):
        folder = tempfile.TemporaryDirectory()
        self.addCleanup(folder.cleanup)
        path = Path(folder.name) / "data.tar"
        with tarfile.open(path, "w") as archive:
            for name, value in entries:
                item = tarfile.TarInfo(name)
                item.size = len(value)
                archive.addfile(item, io.BytesIO(value))
        return path

    def test_payloads_and_appended_journal_survive(self):
        before = inventory(self.archive([("events.jsonl", b'{"id":1}\n'), ("files/user.txt", b"authored")]))
        after = self.archive([("events.jsonl", b'{"id":1}\n{"id":2}\n'),
                              ("files/user.txt", b"authored"), ("new.txt", b"new")])
        self.assertEqual(inventory(after, before), before)

    def test_rejects_changed_missing_and_truncated_originals(self):
        before = inventory(self.archive([("files/user.txt", b"authored")]))
        for entries in [[("files/user.txt", b"changed!")], [], [("files/user.txt", b"a")]]:
            with self.subTest(entries=entries), self.assertRaises(ValueError):
                inventory(self.archive(entries), before)

    def test_rejects_traversal_absolute_and_duplicate_names(self):
        for entries in [[("../escape", b"x")], [("/escape", b"x")], [("x", b"a"), ("./x", b"b")]]:
            with self.subTest(entries=entries), self.assertRaises(ValueError):
                inventory(self.archive(entries))

    def test_rejects_link_and_invalid_journal(self):
        path = self.archive([])
        with tarfile.open(path, "w") as archive:
            link = tarfile.TarInfo("link")
            link.type = tarfile.SYMTYPE
            link.linkname = "/outside"
            archive.addfile(link)
        with self.assertRaises(ValueError):
            inventory(path)
        with self.assertRaises(ValueError):
            inventory(self.archive([("events.jsonl", b"invalid json\n")]))


if __name__ == "__main__":
    unittest.main()
