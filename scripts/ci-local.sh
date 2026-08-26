#!/usr/bin/env bash
# Local mirror of ClambHook's GitHub Actions CI gates. This script orchestrates
# the full developer-machine gate; GNU/Linux uses Apple's `container` tool on
# macOS or Docker/Podman on GNU/Linux.
#
#   scripts/ci-local.sh              # all sections
#   scripts/ci-local.sh go linux     # selected sections
#   scripts/ci-local.sh --help
#
# Sections:
#   go      make test, make test-race, make lint
#   apple   make build-apple, make test-apple (swift test)            [macOS only]
#   android make build-android-mobile-aar, test-android, lint-android, build-android
#          + on-device AVD smoke (Android SDK Emulator) when CI_LOCAL_ANDROID_AVD=<name>
#   linux   make test-linux + Trisquel/Rocky/Alma container validation
#   e2e     make e2e-required (local sing-box/Tor/ClambBack) + make e2e-tun (Linux + /dev/net/tun)
#   smoke   make macos-release-contract-check, make package-smoke
#   all     run every section (default)
#
# A section whose required tooling is absent is skipped with a warning (non-fatal).
# A section that runs and fails is recorded; the script exits non-zero if any
# ran section failed. Skips never fail the gate. Set CI_LOCAL_ANDROID_AVD=<name>
# to add an on-device AVD smoke (Android SDK Emulator) to the android section.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

HOST_OS="$(uname -s 2>/dev/null || echo unknown)"
# Distinct sentinel so a real command failure (exit 1/2/124/…) is not mistaken
# for an intentional skip.
SKIP_RC=200

HAVE() { command -v "$1" >/dev/null 2>&1; }
have_pkg() { pkg-config --exists "$1" 2>/dev/null; }

ALL_SECTIONS=(go apple android linux e2e smoke)

usage() {
    sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    printf '\nAvailable sections: %s all\n' "${ALL_SECTIONS[*]}"
    exit 0
}

section_go() {
    HAVE go || {
        echo "ci-local: [go] skip: go not on PATH" >&2
        return "$SKIP_RC"
    }
    have_pkg libsodium || {
        echo "ci-local: [go] skip: libsodium not found via pkg-config" >&2
        return "$SKIP_RC"
    }
    echo "==================== ci-local: go ===================="
    make test
    make test-race
    make lint
}

section_apple() {
    [[ "$HOST_OS" == "Darwin" ]] || {
        echo "ci-local: [apple] skip: not macOS ($HOST_OS)" >&2
        return "$SKIP_RC"
    }
    HAVE xcodebuild || {
        echo "ci-local: [apple] skip: xcodebuild not on PATH (install Xcode)" >&2
        return "$SKIP_RC"
    }
    HAVE xcodegen || {
        echo "ci-local: [apple] skip: xcodegen not on PATH (brew install xcodegen)" >&2
        return "$SKIP_RC"
    }
    echo "==================== ci-local: apple ===================="
    make build-apple
    make test-apple
}

section_android() {
    HAVE java || {
        echo "ci-local: [android] skip: java (JDK 17+) not on PATH" >&2
        return "$SKIP_RC"
    }
    [[ -x ui/android/gradlew ]] || {
        echo "ci-local: [android] skip: ui/android/gradlew missing" >&2
        return "$SKIP_RC"
    }
    echo "==================== ci-local: android ===================="
    # Embedded daemon AAR (gomobile). Skipped if gomobile/gobind are not installed.
    if HAVE gomobile && HAVE gobind; then
        make build-android-mobile-aar
    else
        echo "ci-local: [android] skip: gomobile/gobind not installed (AAR build)" >&2
    fi
    make test-android
    make lint-android
    make build-android
    # On-device smoke on an Android SDK Emulator (AVD) via Google's `android`
    # CLI. Apple `container` is Linux-only and cannot run Android, so the
    # Android on-device CI/CD path uses an AVD. Opt-in (boots an emulator):
    # set CI_LOCAL_ANDROID_AVD=<name>. Create one with
    # `android emulator create --name=clambhook --package="system-images;android-34;google_apis;arm64-v8a"`.
    # The emulator is left running after the run; stop it with
    # `android emulator stop <avd>` when done.
    if ! HAVE android; then
        echo "ci-local: [android] skip: on-device AVD smoke (install the android CLI; see docs/android-development.md)" >&2
    elif [[ -z "${CI_LOCAL_ANDROID_AVD:-}" ]]; then
        echo "ci-local: [android] skip: on-device AVD smoke (set CI_LOCAL_ANDROID_AVD=<name>; see docs/android-development.md)" >&2
    else
        avd="$CI_LOCAL_ANDROID_AVD"
        echo "ci-local: [android] on-device AVD smoke on '$avd' (start emulator → make run-android)..."
        # `android emulator start` blocks until the AVD is booted; tolerate an
        # already-running emulator.
        android emulator start "$avd" || echo "ci-local: [android] note: emulator start returned non-zero (may already be running)" >&2
        if make run-android; then
            echo "ci-local: [android] AVD smoke passed on '$avd'"
        else
            echo "ci-local: [android] AVD smoke failed on '$avd'" >&2
            return 1
        fi
    fi
}

section_linux() {
    echo "==================== ci-local: linux ===================="
    # Host-side Kotlin unit tests for the Compose Multiplatform desktop controller.
    if HAVE java && [[ -x ui/linux/gradlew ]]; then
        make test-linux
    else
        echo "ci-local: [linux] skip: java/ui/linux gradlew missing (host Kotlin tests)" >&2
    fi
    # Cross-distro container validation (Apple container on macOS, podman/docker on Linux).
    if HAVE container || HAVE podman || HAVE docker; then
        scripts/validate-linux-distros.sh
    else
        echo "ci-local: [linux] skip: no container engine (Apple container/podman/docker)" >&2
    fi
}

section_e2e() {
    HAVE go || {
        echo "ci-local: [e2e] skip: go not on PATH" >&2
        return "$SKIP_RC"
    }
    have_pkg libsodium || {
        echo "ci-local: [e2e] skip: libsodium not found via pkg-config" >&2
        return "$SKIP_RC"
    }
    # e2e-required needs local sing-box + Tor (ClambBack is auto-provisioned by
    # the Makefile target). Skip gracefully when they are absent.
    if ! HAVE sing-box || ! HAVE tor; then
        echo "ci-local: [e2e] skip: sing-box and/or tor not installed (required for make e2e-required)" >&2
        return "$SKIP_RC"
    fi
    echo "==================== ci-local: e2e ===================="
    CLAMBHOOK_E2E_BACKEND=auto make e2e-required
    # Privileged TUN round-trip needs /dev/net/tun + CAP_NET_ADMIN + root. Run it
    # on a Linux host; on macOS, run it inside an Apple container with NET_ADMIN.
    if [[ "$HOST_OS" == "Linux" ]] && [[ -c /dev/net/tun ]]; then
        make e2e-tun
    else
        echo "ci-local: [e2e] skip: e2e-tun needs Linux + /dev/net/tun (on macOS, run inside an Apple container with NET_ADMIN)" >&2
    fi
}

section_smoke() {
    echo "==================== ci-local: smoke ===================="
    if [[ "$HOST_OS" == "Darwin" ]]; then
        make macos-release-contract-check
    else
        echo "ci-local: [smoke] skip: macos-release-contract-check is macOS-only" >&2
    fi
    make package-smoke
}

# --- argument parsing ---
sections=()
for arg in "$@"; do
    case "$arg" in
    -h | --help) usage ;;
    all)
        sections=("${ALL_SECTIONS[@]}")
        break
        ;;
    go | apple | android | linux | e2e | smoke) sections+=("$arg") ;;
    *)
        echo "ci-local: unknown section '$arg' (try --help)" >&2
        exit 2
        ;;
    esac
done
if [[ ${#sections[@]} -eq 0 ]]; then
    sections=("${ALL_SECTIONS[@]}")
fi

# --- run sections (each in a subshell so a failure is captured, not fatal) ---
ran=()
failed=()
for s in "${sections[@]}"; do
    rc=0
    (
        set -e
        "section_$s"
    ) || rc=$?
    if [[ $rc -eq 0 ]]; then
        ran+=("$s:ok")
    elif [[ $rc -eq "$SKIP_RC" ]]; then
        ran+=("$s:skip")
    else
        failed+=("$s")
        ran+=("$s:FAIL($rc)")
    fi
done

echo "==================== ci-local: summary ===================="
printf '  %s\n' "${ran[@]}"
if [[ ${#failed[@]} -gt 0 ]]; then
    echo "ci-local: FAILED sections: ${failed[*]}" >&2
    exit 1
fi
echo "ci-local: all sections passed (skips are non-fatal)."
