#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

"""Select Substrate's GTK Linux backend on native AArch64 desktop hosts.

Gluon Substrate 0.0.69 equates the AArch64 architecture with its
Raspberry Pi/Monocle backend in LinuxTargetConfiguration. ClambHook builds on
native Ubuntu and Fedora AArch64 hosts, so only that class-local backend test
is changed. The target triplet remains AArch64 everywhere else.
"""

from __future__ import annotations

import hashlib
import pathlib
import struct
import sys
import tempfile
from typing import NoReturn
import zipfile


OFFICIAL_SHA256 = "d2ba5f26578e4aa81e358f2e9fdf107c1d528294920db4e4a70841a678e49cf4"
CLASS_ENTRY = "com/gluonhq/substrate/target/LinuxTargetConfiguration.class"
ORIGINAL_SELECTOR = b"aarch64"
GTK_SELECTOR = b"gtkarch"


def fail(message: str) -> NoReturn:
    raise SystemExit(f"Substrate AArch64 patch: {message}")


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def constant_pool_utf8_offsets(class_file: bytes) -> list[tuple[int, int]]:
    if len(class_file) < 10 or class_file[:4] != b"\xca\xfe\xba\xbe":
        fail(f"{CLASS_ENTRY} is not a Java class file")

    count = struct.unpack_from(">H", class_file, 8)[0]
    offsets: list[tuple[int, int]] = []
    offset = 10
    index = 1
    fixed_sizes = {
        3: 4,
        4: 4,
        5: 8,
        6: 8,
        7: 2,
        8: 2,
        9: 4,
        10: 4,
        11: 4,
        12: 4,
        15: 3,
        16: 2,
        17: 4,
        18: 4,
        19: 2,
        20: 2,
    }

    while index < count:
        if offset >= len(class_file):
            fail(f"{CLASS_ENTRY} has a truncated constant pool")
        tag = class_file[offset]
        offset += 1
        if tag == 1:
            if offset + 2 > len(class_file):
                fail(f"{CLASS_ENTRY} has a truncated UTF-8 constant")
            length = struct.unpack_from(">H", class_file, offset)[0]
            offset += 2
            if offset + length > len(class_file):
                fail(f"{CLASS_ENTRY} has a truncated UTF-8 payload")
            offsets.append((offset, length))
            offset += length
        elif tag in fixed_sizes:
            offset += fixed_sizes[tag]
            if offset > len(class_file):
                fail(f"{CLASS_ENTRY} has a truncated constant")
            if tag in (5, 6):
                index += 1
        else:
            fail(f"{CLASS_ENTRY} uses unsupported constant-pool tag {tag}")
        index += 1

    return offsets


def patch_class(class_file: bytes) -> bytes:
    patched = bytearray(class_file)
    original_matches = 0
    patched_matches = 0
    for offset, length in constant_pool_utf8_offsets(class_file):
        value = class_file[offset : offset + length]
        if value == ORIGINAL_SELECTOR:
            original_matches += 1
            patched[offset : offset + length] = GTK_SELECTOR
        elif value == GTK_SELECTOR:
            patched_matches += 1

    if original_matches != 1 or patched_matches != 0:
        fail(
            "expected exactly one unmodified architecture selector; "
            f"found original={original_matches}, patched={patched_matches}"
        )
    return bytes(patched)


def patch_jar(source: pathlib.Path, destination: pathlib.Path) -> None:
    actual_hash = sha256(source)
    if actual_hash != OFFICIAL_SHA256:
        fail(
            f"official Substrate SHA-256 mismatch for {source}: "
            f"expected {OFFICIAL_SHA256}, got {actual_hash}"
        )

    destination.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(source, "r") as source_jar:
        entries = source_jar.infolist()
        if sum(entry.filename == CLASS_ENTRY for entry in entries) != 1:
            fail(f"expected exactly one {CLASS_ENTRY} entry")

        with tempfile.NamedTemporaryFile(
            dir=destination.parent, prefix=f".{destination.name}.", delete=False
        ) as temporary:
            temporary_path = pathlib.Path(temporary.name)

        try:
            with zipfile.ZipFile(temporary_path, "w") as output_jar:
                output_jar.comment = source_jar.comment
                for entry in entries:
                    payload = source_jar.read(entry)
                    if entry.filename == CLASS_ENTRY:
                        payload = patch_class(payload)
                    output_jar.writestr(entry, payload)

            with zipfile.ZipFile(temporary_path, "r") as check_jar:
                patched_class = check_jar.read(CLASS_ENTRY)
                values = [
                    patched_class[offset : offset + length]
                    for offset, length in constant_pool_utf8_offsets(patched_class)
                ]
                if values.count(ORIGINAL_SELECTOR) != 0 or values.count(GTK_SELECTOR) != 1:
                    fail("patched class did not retain exactly one GTK selector")
            temporary_path.replace(destination)
        finally:
            temporary_path.unlink(missing_ok=True)


def main() -> None:
    if len(sys.argv) != 3:
        fail("usage: patch-gluon-substrate-aarch64.py INPUT_JAR OUTPUT_JAR")
    source = pathlib.Path(sys.argv[1]).resolve()
    destination = pathlib.Path(sys.argv[2]).resolve()
    if source == destination:
        fail("input and output JAR paths must differ")
    patch_jar(source, destination)
    print(f"Prepared GTK-selecting Substrate artifact: {destination}")


if __name__ == "__main__":
    main()
