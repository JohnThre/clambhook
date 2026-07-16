#!/bin/sh
# Xcode Cloud post-clone step for ClambHook's Apple platforms (macOS first; the
# same script serves iOS/iPadOS/watchOS/visionOS targets when added).
#
# Xcode Cloud runs ci_scripts/ci_post_clone.sh automatically after cloning the
# repository and before resolving dependencies. The Apple project is generated
# with XcodeGen and the Go daemon runtime is produced by the Makefile, so this
# script prepares both so the cloud build has a real project + embedded runtime
# to compile. See docs/release-validation.md.
set -eu

# Xcode Cloud checks the repo out under $CI_WORKSPACE (or $CI_PRIMARY_REPOSITORY_PATH).
REPO_ROOT="${CI_PRIMARY_REPOSITORY_PATH:-${CI_WORKSPACE:-$(cd "$(dirname "$0")/.." && pwd)}}"
cd "$REPO_ROOT"

echo "ClambHook Xcode Cloud post-clone: preparing Apple runtime + project"

# Toolchains Xcode Cloud does not provide by default.
if ! command -v go >/dev/null 2>&1; then
  echo "Installing Go via Homebrew for the embedded daemon build"
  brew install go
fi
if ! command -v xcodegen >/dev/null 2>&1; then
  echo "Installing XcodeGen"
  brew install xcodegen
fi
if ! command -v pkg-config >/dev/null 2>&1; then
  brew install pkg-config
fi
# libsodium is required by the C kernel (clib) reached through cgo.
if ! pkg-config --exists libsodium 2>/dev/null; then
  brew install libsodium
fi

# Build the darwin daemon + terminal UI runtime, then generate the Xcode project.
make prepare-apple-runtime
make generate-apple

echo "Post-clone complete: ui/apple project generated and runtime staged."
