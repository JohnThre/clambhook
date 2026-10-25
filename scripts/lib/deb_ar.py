#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

"""Minimal ar(5) support for embedding debsigs signatures without debsigs.

    deb_ar.py origin-payload <package.deb> <output>
        Write the bytes a debsigs "origin" signature covers: debian-binary,
        control.tar.*, and data.tar.* concatenated in archive order.
    deb_ar.py signed-payload <package.deb> <output>
        Write the same bytes from an already signed package, for verification.
    deb_ar.py append <package.deb> <member-file> <member-name>
        Append a member, matching the archive's existing name style.
    deb_ar.py extract <package.deb> <member-name> <output>
        Copy one member's bytes.

Used only on hosts without dpkg/debsigs (the maintainer's Mac); the real
debsig-verify still checks every package in a container afterwards.
"""

from __future__ import annotations

import pathlib
import sys
import time

MAGIC = b"!<arch>\n"
HEADER = 60


def fail(message: str) -> None:
    print(f"deb_ar: {message}", file=sys.stderr)
    raise SystemExit(1)


def members(data: bytes) -> list[tuple[str, bytes, bool]]:
    if not data.startswith(MAGIC):
        fail("not an ar archive")
    offset = len(MAGIC)
    found = []
    while offset < len(data):
        header = data[offset:offset + HEADER]
        if len(header) != HEADER or header[58:60] != b"`\n":
            fail("malformed ar member header")
        raw_name = header[0:16].decode("ascii").rstrip(" ")
        slashed = raw_name.endswith("/")
        name = raw_name.rstrip("/")
        size = int(header[48:58].decode("ascii").strip())
        start = offset + HEADER
        found.append((name, data[start:start + size], slashed))
        offset = start + size + (size % 2)
    return found


def origin_payload(package: pathlib.Path, output: pathlib.Path, signed: bool = False) -> None:
    parts = members(package.read_bytes())
    names = [name for name, _, _ in parts]
    if not names or names[0] != "debian-binary":
        fail("debian-binary must be the first member")
    has_signature = any(name.startswith("_gpg") for name in names)
    if has_signature != signed:
        fail("package already carries a debsigs signature" if has_signature
             else "package carries no debsigs signature")
    wanted = [body for name, body, _ in parts
              if name == "debian-binary" or name.startswith(("control.tar", "data.tar"))]
    if len(wanted) != 3:
        fail("expected debian-binary, control.tar.* and data.tar.* members")
    output.write_bytes(b"".join(wanted))


def append(package: pathlib.Path, member: pathlib.Path, name: str) -> None:
    data = package.read_bytes()
    existing = members(data)
    slashed = existing[0][2] if existing else False
    label = f"{name}/" if slashed else name
    if len(label) > 16:
        fail("member name is too long")
    body = member.read_bytes()
    header = (
        label.ljust(16)
        + str(int(time.time())).ljust(12)
        + "0".ljust(6)
        + "0".ljust(6)
        + "100644".ljust(8)
        + str(len(body)).ljust(10)
        + "`\n"
    ).encode("ascii")
    if not data.endswith(b"\n") and len(data) % 2:
        data += b"\n"
    data += header + body + (b"\n" if len(body) % 2 else b"")
    package.write_bytes(data)


def extract(package: pathlib.Path, name: str, output: pathlib.Path) -> None:
    for member_name, body, _ in members(package.read_bytes()):
        if member_name == name:
            output.write_bytes(body)
            return
    fail(f"member {name} not found")


def main() -> None:
    if len(sys.argv) < 2:
        fail("usage: deb_ar.py origin-payload|append|extract ...")
    command, args = sys.argv[1], sys.argv[2:]
    if command == "origin-payload" and len(args) == 2:
        origin_payload(pathlib.Path(args[0]), pathlib.Path(args[1]))
    elif command == "signed-payload" and len(args) == 2:
        origin_payload(pathlib.Path(args[0]), pathlib.Path(args[1]), signed=True)
    elif command == "append" and len(args) == 3:
        append(pathlib.Path(args[0]), pathlib.Path(args[1]), args[2])
    elif command == "extract" and len(args) == 3:
        extract(pathlib.Path(args[0]), args[1], pathlib.Path(args[2]))
    else:
        fail("invalid arguments")


if __name__ == "__main__":
    main()
