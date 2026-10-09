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
    "android-gradle-plugin": "9.3.2",
    "Gradle": "9.7.1",
    "kotlin-serialization-plugin": "2.4.10",
    "kotlin-multiplatform-plugin": "2.4.10",
    "kotlin-stdlib": "2.4.10",
    "compose-gradle-plugin": "1.11.1",
    "runtime": "1.11.1",
    "foundation": "1.11.1",
    "ui": "1.11.1",
    "material3": "1.9.0",
    "activity-compose": "1.13.0",
    "core-ktx": "1.18.0",
    "datastore-preferences": "1.1.1",
    "security-crypto": "1.1.0",
    "kotlinx-coroutines-android": "1.11.0",
    "kotlinx-coroutines-swing": "1.11.0",
    "kotlinx-serialization-json": "1.11.0",
    "okhttp": "5.4.0",
    "zxing-android-embedded": "4.3.0",
    "bcpg-jdk18on": "1.86",
}
actual = {component.get("name"): component.get("version") for component in components}
for name, version in expected.items():
    if actual.get(name) != version:
        fail(f"missing or stale component: {name} {version}")
for retired in ("javafx-controls", "javafx-static-sdk", "substrate", "gluonfx-maven-plugin"):
    if retired in actual:
        fail(f"retired JavaFX/Gluon component remains: {retired}")

KOTLIN = ROOT / "ui" / "kotlin"
root_gradle = (KOTLIN / "build.gradle.kts").read_text(encoding="utf-8")
platform_gradle = (KOTLIN / "platform" / "build.gradle.kts").read_text(encoding="utf-8")
shared_gradle = (KOTLIN / "shared" / "build.gradle.kts").read_text(encoding="utf-8")
app_gradle = (KOTLIN / "app" / "build.gradle.kts").read_text(encoding="utf-8")
gradle_wrapper = (KOTLIN / "gradle" / "wrapper" / "gradle-wrapper.properties").read_text(
    encoding="utf-8"
)
pins = {
    "build.gradle.kts": (root_gradle, ("9.3.2", "2.4.10", '"1.11.1"')),
    "platform/build.gradle.kts": (
        platform_gradle,
        ("core-ktx:1.18.0", "datastore-preferences:1.1.1", "security-crypto:1.1.0",
         "coroutines-android:1.11.0", "serialization-json:1.11.0", "okhttp:5.4.0",
         "zxing-android-embedded:4.3.0", "bcpg-jdk18on:1.86"),
    ),
    "shared/build.gradle.kts": (
        shared_gradle,
        ('composeVersion = "1.11.1"', "material3:1.9.0", "coroutines-swing:1.11.0"),
    ),
    "app/build.gradle.kts": (app_gradle, ("activity-compose:1.13.0", "core-ktx:1.18.0")),
}
for label, (text, values) in pins.items():
    for value in values:
        if value not in text:
            fail(f"dependency pin {value} is absent from ui/kotlin/{label}")
for value in (
    "gradle-9.7.1-bin.zip",
    "acd53f1edaf02f1a8ff99879f8a34b302661a057d9b063ae9e35b552f804d20a",
):
    if value not in gradle_wrapper:
        fail(f"Gradle wrapper pin is absent from gradle-wrapper.properties: {value}")

dependency_refs = {
    reference
    for dependency in document.get("dependencies", [])
    for reference in dependency.get("dependsOn", [])
}
unknown = dependency_refs.difference(references)
if unknown:
    fail("dependency graph contains unknown references: " + ", ".join(sorted(unknown)))

print(f"SBOM policy: {len(components)} components validated")
