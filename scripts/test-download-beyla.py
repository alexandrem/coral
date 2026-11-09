#!/usr/bin/env python3
"""Offline regression checks: python3 scripts/test-download-beyla.py."""

import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest


class DownloadBeylaTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        scripts = self.root / "scripts"
        scripts.mkdir()
        self.script = scripts / "download-beyla.sh"
        shutil.copyfile(Path(__file__).with_name("download-beyla.sh"), self.script)
        self.binaries = self.root / "internal/agent/beyla/binaries"
        self.binaries.mkdir(parents=True)

        # Supply a tiny release archive and intercept curl, so these checks
        # exercise the real downloader without network access or large binaries.
        archive = self.root / "release.tar.gz"
        with tarfile.open(archive, "w:gz") as release:
            content = b"new beyla binary\n"
            binary = tarfile.TarInfo("beyla")
            binary.size = len(content)
            release.addfile(binary, io.BytesIO(content))
        stub_dir = self.root / "bin"
        stub_dir.mkdir()
        curl = stub_dir / "curl"
        curl.write_text("""#!/bin/bash
set -eu
echo "$*" >> "$DOWNLOAD_LOG"
if [ "${FAIL_DOWNLOAD:-0}" = 1 ]; then exit 22; fi
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then
        cp "$RELEASE_ARCHIVE" "$2"
        exit 0
    fi
    shift
done
exit 1
""")
        curl.chmod(0o755)
        self.log = self.root / "downloads.log"
        self.env = dict(os.environ, PATH=f"{stub_dir}:{os.environ['PATH']}",
                        RELEASE_ARCHIVE=str(archive), DOWNLOAD_LOG=str(self.log))
        self.env.pop("BEYLA_VERSION", None)
        self.env.pop("FAIL_DOWNLOAD", None)

    def run_download(self, version="v3.0.0", **env):
        return subprocess.run(
            ["bash", str(self.script)],
            env=dict(self.env, BEYLA_VERSION=version, **env),
            capture_output=True, text=True, check=False,
        )

    def seed_cache(self, version=None):
        for platform in ("linux-amd64", "linux-arm64"):
            binary = self.binaries / f"beyla-{platform}"
            binary.write_text("old beyla binary\n")
            binary.chmod(0o755)
            if version:
                Path(f"{binary}.version").write_text(f"{version}\n")

    def assert_updated(self, version):
        for platform in ("linux-amd64", "linux-arm64"):
            binary = self.binaries / f"beyla-{platform}"
            self.assertEqual(binary.read_text(), "new beyla binary\n")
            self.assertTrue(os.access(binary, os.X_OK))
            self.assertEqual(Path(f"{binary}.version").read_text(), f"{version}\n")
        downloads = self.log.read_text().splitlines()
        self.assertEqual(len(downloads), 2)
        self.assertTrue(all(f"/{version}/" in line for line in downloads))

    def test_fresh_download(self):
        result = self.run_download()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_updated("v3.0.0")

    def test_unversioned_binary_is_replaced(self):
        self.seed_cache()
        result = self.run_download()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_updated("v3.0.0")

    def test_version_change_refreshes_both_platforms(self):
        self.seed_cache("v2.7.8")
        result = self.run_download("v9.9.9")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_updated("v9.9.9")

    def test_matching_cache_avoids_network(self):
        self.seed_cache("v3.0.0")
        result = self.run_download(FAIL_DOWNLOAD="1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.log.exists())

    def test_failed_upgrade_stops_build_and_preserves_old_binary(self):
        self.seed_cache("v2.7.8")
        result = self.run_download(FAIL_DOWNLOAD="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.log.read_text().splitlines()), 1)
        binary = self.binaries / "beyla-linux-amd64"
        self.assertEqual(binary.read_text(), "old beyla binary\n")
        self.assertEqual(Path(f"{binary}.version").read_text(), "v2.7.8\n")

    def test_invalid_archive_is_not_cached(self):
        Path(self.env["RELEASE_ARCHIVE"]).write_bytes(b"not an archive")
        result = self.run_download()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(self.binaries.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
