#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

"""Check an unsigned apt/dnf repository tree before the host signs it.

    verify_repo_tree.py <repo-dir> <channel> <allowlist>

<allowlist> holds one SHA-256 per line: the packages the host itself signed
and verified. The tree was generated in a container, so nothing in it is
trusted: every package must be on the allowlist, every hash in Release,
Packages, repomd.xml and primary.xml must match the file it names, and no
unexpected files may be present. Only then may the metadata be signed.
"""

from __future__ import annotations

import gzip
import hashlib
import pathlib
import sys
import xml.etree.ElementTree as ET

REPO_NS = {"repo": "http://linux.duke.edu/metadata/repo"}
COMMON_NS = {"common": "http://linux.duke.edu/metadata/common"}


def fail(message: str) -> None:
    print(f"verify-repo-tree: {message}", file=sys.stderr)
    raise SystemExit(1)


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_xml(raw: bytes, label: str) -> ET.Element:
    # The metadata comes from an untrusted container: refuse DTDs and entity
    # declarations outright instead of relying on the parser's defaults.
    if b"<!DOCTYPE" in raw or b"<!ENTITY" in raw:
        fail(f"{label} contains a DTD or entity declaration")
    return ET.fromstring(raw)


def child(element: ET.Element, path: str, ns: dict[str, str], label: str) -> ET.Element:
    found = element.find(path, ns)
    if found is None:
        fail(f"{label} lacks {path}")
    return found


def attr(element: ET.Element, name: str, label: str) -> str:
    value = element.get(name)
    if not value:
        fail(f"{label} lacks the {name} attribute")
    return value


def text(element: ET.Element, label: str) -> str:
    if not element.text or not element.text.strip():
        fail(f"{label} is empty")
    return element.text.strip()


def inside(root: pathlib.Path, relative: str) -> pathlib.Path:
    path = (root / relative).resolve()
    if root.resolve() not in path.parents and path != root.resolve():
        fail(f"path escapes the repository: {relative}")
    return path


def check_apt(apt: pathlib.Path, channel: str, allowed: set[str], seen: set[pathlib.Path]) -> None:
    dists = apt / "dists" / channel
    release = dists / "Release"
    if not release.is_file():
        fail("apt Release is missing")
    seen.add(release.resolve())
    in_sha256 = False
    listed = 0
    for line in release.read_text(encoding="utf-8").splitlines():
        if not line.startswith(" "):
            in_sha256 = line.strip() == "SHA256:"
            continue
        if not in_sha256:
            continue
        digest, size, name = line.split()
        target = inside(dists, name)
        if not target.is_file():
            fail(f"Release lists a missing file: {name}")
        if sha256(target) != digest or target.stat().st_size != int(size):
            fail(f"Release hash does not match {name}")
        seen.add(target)
        listed += 1
    if listed == 0:
        fail("Release has no SHA256 entries")

    for packages in dists.glob("main/binary-*/Packages"):
        entry: dict[str, str] = {}
        stanzas = packages.read_text(encoding="utf-8").split("\n\n")
        for stanza in stanzas:
            entry = {}
            for line in stanza.splitlines():
                if ":" in line and not line.startswith(" "):
                    key, value = line.split(":", 1)
                    entry[key] = value.strip()
            if not entry:
                continue
            filename, digest = entry.get("Filename", ""), entry.get("SHA256", "")
            if not filename or not digest:
                fail(f"Packages stanza without Filename/SHA256 in {packages}")
            target = inside(apt, filename)
            if digest not in allowed or sha256(target) != digest:
                fail(f"apt package is not one the host signed: {filename}")
            seen.add(target)
    for deb in apt.glob("pool/**/*.deb"):
        if sha256(deb) not in allowed:
            fail(f"unexpected package in the apt pool: {deb.name}")
        seen.add(deb.resolve())


def check_rpm(rpm_root: pathlib.Path, allowed: set[str], seen: set[pathlib.Path]) -> None:
    for arch_dir in sorted(p for p in rpm_root.iterdir() if p.is_dir()):
        repomd = arch_dir / "repodata" / "repomd.xml"
        if not repomd.is_file():
            fail(f"repomd.xml is missing for {arch_dir.name}")
        seen.add(repomd.resolve())
        primary_path: pathlib.Path | None = None
        root = parse_xml(repomd.read_bytes(), "repomd.xml")
        for data in root.findall("repo:data", REPO_NS):
            href = attr(child(data, "repo:location", REPO_NS, "repomd data"), "href", "repomd location")
            checksum = child(data, "repo:checksum", REPO_NS, "repomd data")
            if checksum.get("type") != "sha256":
                fail(f"repomd.xml does not use sha256 for {href}")
            target = inside(arch_dir, href)
            if not target.is_file() or sha256(target) != text(checksum, "repomd checksum"):
                fail(f"repomd.xml hash does not match {href}")
            seen.add(target)
            if data.get("type") == "primary":
                primary_path = target
        if primary_path is None:
            fail(f"repomd.xml for {arch_dir.name} has no primary metadata")
            return
        raw = primary_path.read_bytes()
        if primary_path.suffix == ".gz":
            raw = gzip.decompress(raw)
        primary = parse_xml(raw, "primary.xml")
        for package in primary.findall("common:package", COMMON_NS):
            href = attr(child(package, "common:location", COMMON_NS, "package"), "href", "package location")
            checksum = child(package, "common:checksum", COMMON_NS, "package")
            digest = text(checksum, "package checksum")
            target = inside(arch_dir, href)
            if (checksum.get("type") != "sha256" or digest not in allowed
                    or not target.is_file() or sha256(target) != digest):
                fail(f"rpm package is not one the host signed: {href}")
            seen.add(target)
        for rpm in arch_dir.glob("*.rpm"):
            if sha256(rpm) not in allowed:
                fail(f"unexpected package in the dnf repository: {rpm.name}")
            seen.add(rpm.resolve())


def main() -> None:
    if len(sys.argv) != 4:
        fail("usage: verify_repo_tree.py <repo-dir> <channel> <allowlist>")
    repo = pathlib.Path(sys.argv[1]).resolve()
    channel = sys.argv[2]
    allowed = {line.strip() for line in pathlib.Path(sys.argv[3]).read_text().splitlines() if line.strip()}
    if not allowed:
        fail("empty allowlist")
    seen: set[pathlib.Path] = {(repo / "clambhook-release-key.asc").resolve()}
    check_apt(repo / "apt", channel, allowed, seen)
    check_rpm(repo / "rpm" / channel, allowed, seen)
    # Everything must be accounted for: Packages/Packages.gz are listed in
    # Release and every repodata file in repomd.xml.
    unexpected = [p for p in repo.rglob("*") if p.is_file() and p.resolve() not in seen]
    if unexpected:
        fail("unexpected files in the repository: " + ", ".join(str(p.relative_to(repo)) for p in unexpected))
    print(f"verify-repo-tree: {repo} matches {len(allowed)} host-verified packages")


if __name__ == "__main__":
    main()
