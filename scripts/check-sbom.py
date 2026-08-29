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
    "libmaxminddb": "1.13.3",
    "wireguard-lwip": "c54f20dbe76ac8b3411ad21e0ed7deea6f0cfd4d",
    "OpenSSL": "3.5.8",
    "curl": "8.18.0",
    "javafx-controls": "21.0.12",
    "javafx-static-sdk": "21.0.1",
    "substrate": "0.0.69",
    "gluonfx-maven-plugin": "1.0.29",
    "gradle": "8.9.1",
    "kotlin-gradle-plugin": "2.3.20",
    "GraalVM Community Edition": "17.0.9",
    "core-ktx": "1.16.0",
    "datastore-preferences": "1.1.1",
    "security-crypto": "1.1.0-alpha06",
    "kotlinx-coroutines-android": "1.9.0",
    "kotlinx-serialization-json": "1.7.3",
    "okhttp": "4.12.0",
    "zxing-android-embedded": "4.3.0",
}
actual = {component.get("name"): component.get("version") for component in components}
for name, version in expected.items():
    if actual.get(name) != version:
        fail(f"missing or stale component: {name} {version}")

pom = (ROOT / "ui" / "javafx" / "pom.xml").read_text(encoding="utf-8")
gradle = (ROOT / "ui" / "android" / "app" / "build.gradle.kts").read_text(
    encoding="utf-8"
)
root_gradle = (ROOT / "ui" / "android" / "build.gradle.kts").read_text(
    encoding="utf-8"
)
for value in ("21.0.12", "21.0.1", "1.0.29"):
    if value not in pom:
        fail(f"JavaFX/Gluon pin {value} is absent from pom.xml")
for value in (
    "1.16.0",
    "1.1.1",
    "1.1.0-alpha06",
    "1.9.0",
    "1.7.3",
    "4.12.0",
    "4.3.0",
):
    if value not in gradle:
        fail(f"Android dependency pin {value} is absent from app/build.gradle.kts")
for value in ("8.9.1", "2.3.20"):
    if value not in root_gradle:
        fail(f"Android build-tool pin {value} is absent from build.gradle.kts")

dependency_refs = {
    reference
    for dependency in document.get("dependencies", [])
    for reference in dependency.get("dependsOn", [])
}
unknown = dependency_refs.difference(references)
if unknown:
    fail("dependency graph contains unknown references: " + ", ".join(sorted(unknown)))

print(f"SBOM policy: {len(components)} components validated")
