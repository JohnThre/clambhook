#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

"""Validate the deterministic cutover SBOM against pinned source manifests."""

from __future__ import annotations

import json
import pathlib
import sys


ROOT = pathlib.Path(__file__).resolve().parent.parent
SBOM = ROOT / "packaging" / "sbom.cdx.json"


def fail(message: str) -> None:
    print(f"SBOM policy: {message}", file=sys.stderr)
    raise SystemExit(1)


try:
    document = json.loads(SBOM.read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError) as error:
    fail(f"cannot parse {SBOM.relative_to(ROOT)}: {error}")

if document.get("bomFormat") != "CycloneDX" or document.get("specVersion") != "1.6":
    fail("packaging/sbom.cdx.json must be CycloneDX 1.6")

metadata = document.get("metadata", {})
application = metadata.get("component", {})
if application.get("name") != "clambhook" or application.get("version") != "1.0.2":
    fail("application component or version is stale")

components = document.get("components", [])
references = [component.get("bom-ref", "") for component in components]
if not references or any(not reference for reference in references):
    fail("every component must have a bom-ref")
if len(references) != len(set(references)):
    fail("component bom-ref values must be unique")

expected = {
    "lwIP": "2.2.1",
    "tomlc99": "29076dfd095bbbbd50a3c1b2760d29f4b83e74ac",
    "llhttp": "9.4.3",
    "libmaxminddb": "1.13.3",
    "wireguard-lwip": "c54f20dbe76ac8b3411ad21e0ed7deea6f0cfd4d",
    "OpenSSL": "3.5.8",
    "curl": "8.18.0",
}
actual = {component.get("name"): component.get("version") for component in components}
for name, version in expected.items():
    if actual.get(name) != version:
        fail(f"missing or stale component: {name} {version}")
for retired in ("javafx-controls", "javafx-static-sdk", "substrate", "gluonfx-maven-plugin"):
    if retired in actual:
        fail(f"retired JavaFX/Gluon component remains: {retired}")

# Client dependencies (Kotlin, Compose, Android, OpenJDK) belong to the
# private clambhook-apps repository and must not reappear in the core SBOM.
for name in actual:
    if name and (name.startswith("kotlin") or name in {"Gradle", "okhttp", "material3"}):
        fail(f"client component belongs in clambhook-apps: {name}")

dependency_refs = {
    reference
    for dependency in document.get("dependencies", [])
    for reference in dependency.get("dependsOn", [])
}
unknown = dependency_refs.difference(references)
if unknown:
    fail("dependency graph contains unknown references: " + ", ".join(sorted(unknown)))

print(f"SBOM policy: {len(components)} components validated")
