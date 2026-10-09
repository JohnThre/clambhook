# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

.PHONY: all build build-clib build-daemon build-tui build-license build-native \
	test-native test-android test-android-compatibility test-linux \
	build-linux build-linux-package install install-linux prepare-apple-runtime \
	generate-apple build-apple test-apple check-macos-signing release-macos \
	release-linux release-check ci-local macos-release-contract-check \
	package-smoke build-android-platform build-android-native build-android \
	lint-android run-android build-android-release release-android test lint clean

PREFIX ?= /usr/local
DESTDIR ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
NATIVE_BUILD_DIR ?= build-native
NATIVE_SANITIZE_DIR ?= build-native-sanitize
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
ANDROID_NDK_VERSION ?= 28.2.13676358
ANDROID_SDK ?= $(ANDROID_HOME)
ANDROID_NDK ?= $(ANDROID_SDK)/ndk/$(ANDROID_NDK_VERSION)
CLAMBHOOK_HOST_OS ?= $(shell uname -s)
GRADLE = ./gradlew --no-daemon
LINUX_UI_DIST ?= ui/kotlin/desktop/build/compose/binaries/main-release/app/clambhook-ui

require-command = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is required for $(2)." >&2; echo "$(3)" >&2; exit 2; }
internal-release-notice = @printf '%s\n' "local build only: publishing is performed by the protected GitHub Release workflow."

all: build

build-clib:
	$(MAKE) -C clib

build-native:
	cmake -S . -B "$(NATIVE_BUILD_DIR)" -G Ninja \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo -DCLAMBHOOK_ENABLE_SANITIZERS=OFF
	cmake --build "$(NATIVE_BUILD_DIR)"

build-daemon: build-native
	@test -x "$(NATIVE_BUILD_DIR)/clambhook"

build-tui: build-native
	@test -x "$(NATIVE_BUILD_DIR)/clambhook-tui"

build-license: build-native
	@test -x "$(NATIVE_BUILD_DIR)/clambhook-license"

build: build-native

test-native:
	cmake -S . -B "$(NATIVE_SANITIZE_DIR)" -G Ninja \
		-DCMAKE_BUILD_TYPE=Debug -DCLAMBHOOK_ENABLE_SANITIZERS=ON \
		-DCLAMBHOOK_ENABLE_MEMORY_TESTING=ON
	cmake --build "$(NATIVE_SANITIZE_DIR)"
	ctest --test-dir "$(NATIVE_SANITIZE_DIR)" --output-on-failure
	cmake --build "$(NATIVE_SANITIZE_DIR)" --target license-contract

# Kotlin/Compose desktop and shared UI tests. Headless GNU/Linux hosts run
# them under Xvfb.
test-linux:
	@if [ "$(CLAMBHOOK_HOST_OS)" = "Linux" ] && [ -z "$${DISPLAY:-}" ]; then \
		command -v xvfb-run >/dev/null 2>&1 || { \
			echo "xvfb-run is required for headless GNU/Linux UI tests." >&2; \
			exit 2; \
		}; \
		cd ui/kotlin && timeout --kill-after=15s 20m xvfb-run -a $(GRADLE) :shared:desktopTest; \
	else \
		cd ui/kotlin && $(GRADLE) :shared:desktopTest; \
	fi

check-linux-ui-deps:
	@test "$$(uname -s)" = "Linux" || { echo "GNU/Linux (Ubuntu or Fedora) is required for the desktop target." >&2; exit 2; }
	$(call require-command,jpackage,the GNU/Linux desktop distributable,Install a JDK 17+ with jpackage and jmods.)

# Self-contained Compose Desktop distributable with a jlink runtime; no system
# JRE is required at run time.
build-linux: check-linux-ui-deps
	cd ui/kotlin && VERSION="$(VERSION)" $(GRADLE) :desktop:createReleaseDistributable

install: build-native
	DESTDIR="$(DESTDIR)" cmake --install "$(NATIVE_BUILD_DIR)" --prefix "$(PREFIX)" --component Runtime

install-linux: install
	@test -x "$(LINUX_UI_DIST)/bin/clambhook-ui" || $(MAKE) build-linux
	@test -x "$(LINUX_UI_DIST)/bin/clambhook-ui" || { echo "desktop distributable not found: $(LINUX_UI_DIST)" >&2; exit 2; }
	rm -rf "$(DESTDIR)$(PREFIX)/lib/clambhook/ui"
	install -d "$(DESTDIR)$(PREFIX)/lib/clambhook" "$(DESTDIR)$(PREFIX)/bin"
	cp -a "$(LINUX_UI_DIST)" "$(DESTDIR)$(PREFIX)/lib/clambhook/ui"
	ln -sfn ../lib/clambhook/ui/bin/clambhook-ui "$(DESTDIR)$(PREFIX)/bin/clambhook-ui"
	install -d "$(DESTDIR)$(PREFIX)/share/applications"
	sed 's/@app_id@/org.jpfchang.clambhook/g' packaging/desktop/org.jpfchang.clambhook.desktop.in > "$(DESTDIR)$(PREFIX)/share/applications/org.jpfchang.clambhook.desktop"
	install -d "$(DESTDIR)$(PREFIX)/share/metainfo"
	sed 's/@app_id@/org.jpfchang.clambhook/g' packaging/desktop/org.jpfchang.clambhook.metainfo.xml.in > "$(DESTDIR)$(PREFIX)/share/metainfo/org.jpfchang.clambhook.metainfo.xml"
	install -d "$(DESTDIR)$(PREFIX)/share/icons/hicolor/1024x1024/apps"
	install -m 0644 clambhook-icon-1024.png "$(DESTDIR)$(PREFIX)/share/icons/hicolor/1024x1024/apps/org.jpfchang.clambhook.png"
	install -d "$(DESTDIR)$(PREFIX)/lib/systemd/system"
	install -m 0644 packaging/systemd/clambhook-daemon.service "$(DESTDIR)$(PREFIX)/lib/systemd/system/clambhook-daemon.service"
	install -d "$(DESTDIR)$(PREFIX)/share/polkit-1/actions"
	install -m 0644 packaging/polkit/com.clambhook.Clambhook.policy "$(DESTDIR)$(PREFIX)/share/polkit-1/actions/com.clambhook.Clambhook.policy"

prepare-apple-runtime: build-daemon build-tui
	./scripts/prepare-macos-runtime.sh

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: prepare-apple-runtime
	$(MAKE) generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build

test-apple:
	swift test --package-path ui/apple

check-macos-signing:
	./scripts/check-macos-signing.sh

release-macos: macos-release-contract-check check-macos-signing
	$(internal-release-notice)
	./scripts/release-macos.sh

release-linux:
	$(internal-release-notice)
	./scripts/release-linux.sh

release-check:
	$(internal-release-notice)
	$(MAKE) test lint package-smoke macos-release-contract-check

ci-local:
	./scripts/ci-local.sh

macos-release-contract-check:
	$(internal-release-notice)
	./scripts/macos-release-contract-check.sh

package-smoke:
	$(internal-release-notice)
	./scripts/package-smoke.sh

build-android-platform:
	cd ui/kotlin && ANDROID_HOME="$(ANDROID_HOME)" $(GRADLE) :platform:assembleRelease

build-android-native:
	cd ui/kotlin && ANDROID_HOME="$(ANDROID_HOME)" $(GRADLE) :platform:externalNativeBuildDebug

test-android:
	cd ui/kotlin && ANDROID_HOME="$(ANDROID_HOME)" $(GRADLE) \
		:platform:testDebugUnitTest :platform:lintDebug :platform:assembleRelease \
		:app:lintDebug :app:assembleRelease
	./scripts/check-android-abi-policy.sh --require-release

test-android-compatibility:
	cd ui/kotlin && ANDROID_HOME="$(ANDROID_HOME)" $(GRADLE) \
		:platform:androidCompatibilityGroupDebugAndroidTest \
		-Pandroid.experimental.testOptions.managedDevices.maxConcurrentDevices=1 \
		-Pandroid.testoptions.manageddevices.emulator.gpu=software

# ARM64 Kotlin/Compose application (unsigned unless the release keystore
# environment from scripts/prepare-ci-android-signing.sh is present).
build-android:
	@test -d "$(ANDROID_SDK)" || { echo "ANDROID_SDK does not exist: $(ANDROID_SDK)" >&2; exit 2; }
	cd ui/kotlin && ANDROID_HOME="$(ANDROID_HOME)" $(GRADLE) :app:assembleRelease :app:bundleRelease

lint-android:
	cd ui/kotlin && ANDROID_HOME="$(ANDROID_HOME)" $(GRADLE) :platform:lintDebug :app:lintDebug

run-android:
	cd ui/kotlin && android run

build-android-release: build-android
	$(internal-release-notice)

release-android:
	$(internal-release-notice)
	./scripts/release-android.sh

test: test-native test-linux test-android

lint:
	./scripts/lint.sh

clean:
	rm -rf bin/ "$(NATIVE_BUILD_DIR)/" "$(NATIVE_SANITIZE_DIR)/"
	rm -rf ui/apple/Frameworks/*.xcframework
	rm -rf ui/kotlin/build/ ui/kotlin/platform/build/ ui/kotlin/platform/.cxx/ \
		ui/kotlin/shared/build/ ui/kotlin/app/build/ ui/kotlin/desktop/build/
	$(MAKE) -C clib clean
