#!/usr/bin/env python3
"""Build the macOS binaries and the .mypaste-plugin archive.

The archive mirrors every other MyPaste package: the manifest at the root,
the built page beside it, and one entrypoint per macOS architecture under
`bin/darwin/<arch>/` so the host picks the binary that matches the machine.

Usage: python3 scripts/package.py
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "src"
DIST = ROOT / "dist"
ZIP_TIME = (2026, 1, 1, 0, 0, 0)


def manifest() -> dict:
    return json.loads((ROOT / "mypaste.plugin.json").read_text(encoding="utf-8"))


def build_page() -> None:
    subprocess.run(["node", "scripts/build.mjs"], cwd=ROOT, check=True)


def build_binary(target: Path, goarch: str) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, CGO_ENABLED="0", GOOS="darwin", GOARCH=goarch)
    subprocess.run(
        [
            "go", "build", "-trimpath", "-ldflags", "-s -w",
            "-o", str(target), "./cmd/mypaste-translate",
        ],
        cwd=SRC, env=env, check=True,
    )


def add_file(archive: zipfile.ZipFile, name: str, data: bytes, mode: int) -> None:
    info = zipfile.ZipInfo(name, date_time=ZIP_TIME)
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = (0o100000 | mode) << 16
    archive.writestr(info, data)


def main() -> None:
    info = manifest()
    build_page()
    DIST.mkdir(exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix="translate-package-"))
    try:
        for goarch in ("arm64", "amd64"):
            build_binary(staging / "bin" / "darwin" / goarch / "translate", goarch)
        archive_path = DIST / f"{info['id']}-{info['version']}.mypaste-plugin"
        with zipfile.ZipFile(archive_path, "w") as archive:
            add_file(archive, "mypaste.plugin.json",
                     (ROOT / "mypaste.plugin.json").read_bytes(), 0o755)
            add_file(archive, "preview.html", (ROOT / "preview.html").read_bytes(), 0o755)
            for goarch in ("arm64", "amd64"):
                binary = staging / "bin" / "darwin" / goarch / "translate"
                add_file(archive, f"bin/darwin/{goarch}/translate", binary.read_bytes(), 0o755)
        digest = hashlib.sha256(archive_path.read_bytes()).hexdigest()
        Path(f"{archive_path}.sha256").write_text(
            f"{digest}  {archive_path.name}\n", encoding="utf-8")
        size = archive_path.stat().st_size
        print(f"{size} bytes → {archive_path.relative_to(ROOT)}")
        print(f"sha256 {digest}")
    finally:
        shutil.rmtree(staging, ignore_errors=True)


if __name__ == "__main__":
    main()
