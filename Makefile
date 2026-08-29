# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

.PHONY: all build build-clib build-daemon build-tui build-license build-native test-native build-linux-gtk install install-linux prepare-apple-runtime generate-apple build-apple check-macos-signing release-macos release-linux release-check ci-local macos-release-contract-check package-smoke test-apple test-android test-android-compatibility build-android-native build-android lint-android run-android build-android-release release-android check-linux-ui-deps test-linux build-linux test test-race provision-clambback-e2e e2e e2e-required e2e-release e2e-tun lint clean

export CGO_ENABLED=1
PREFIX ?= /usr/local
LINUX_MESON_PREFIX = $(or $(PREFIX),/)
LINUX_MESON_LIBEXECDIR = $(or $(PREFIX),)/libexec
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS ?= -X main.version=$(VERSION)
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
CLAMBBACK_E2E_VERSION ?= 1.0.0-alpha.3
CLAMBBACK_E2E_OS := $(or $(filter Darwin,$(shell uname -s)),Linux)
CLAMBBACK_E2E_SHA256_Linux := 8ec796c909c73d3ad686d53d5b3f6e104d15ce6a0c2514a01622d91e3b46758c
CLAMBBACK_E2E_SHA256_Darwin := 0578e71380ce28f43b983f7f2388c8e13b8424bec8c375774101f4dce405ca27
CLAMBBACK_E2E_SHA256 ?= $(CLAMBBACK_E2E_SHA256_$(CLAMBBACK_E2E_OS))
CLAMBBACK_E2E_BINPATH_Linux := usr/bin/clambback
CLAMBBACK_E2E_BINPATH_Darwin := usr/local/bin/clambback
CLAMBBACK_E2E_ROOT := $(abspath bin/e2e/clambback-$(CLAMBBACK_E2E_VERSION)-$(CLAMBBACK_E2E_OS))
CLAMBBACK_E2E_BIN := $(CLAMBBACK_E2E_ROOT)/$(CLAMBBACK_E2E_BINPATH_$(CLAMBBACK_E2E_OS))
E2E_CLAMBBACK_BIN = $(or $(abspath $(CLAMBBACK_BIN)),$(CLAMBBACK_E2E_BIN))
NATIVE_BUILD_DIR ?= build-native

require-command = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is required for $(2)." >&2; echo "$(3)" >&2; exit 2; }
internal-release-notice = @printf '%s\n' "local build only: publishing is performed by the protected GitHub Release workflow."

all: build

check-linux-ui-deps:
	$(call require-command,java,Linux UI targets,Install JDK 17 or later.)
	@command -v gradle >/dev/null 2>&1 || test -x ui/linux/gradlew || { echo "Gradle is required: install Gradle 8+ or use the bundled ./gradlew in ui/linux." >&2; exit 1; }

# Use the system gradle if available; otherwise fall back to the bundled
# Gradle wrapper.
GRADLE := $(or $(shell command -v gradle 2>/dev/null),./gradlew)

build-clib:
	$(MAKE) -C clib

build-daemon: build-clib
	mkdir -p bin
	go build -ldflags "$(GO_LDFLAGS)" -o bin/clambhook ./cmd/clambhook

build-tui: build-clib
	mkdir -p bin
	go build -ldflags "$(GO_LDFLAGS)" -o bin/clambhook-tui ./cmd/clambhook-tui

build-license:
	mkdir -p bin
	go build -ldflags "$(GO_LDFLAGS)" -o bin/clambhook-license ./cmd/clambhook-license

build: build-daemon build-tui build-license

# Additive C migration targets. The shipping daemon remains on the legacy
# implementation until the documented parity gates pass.
build-native:
	cmake -S . -B "$(NATIVE_BUILD_DIR)" -G Ninja
	cmake --build "$(NATIVE_BUILD_DIR)"

test-native:
	cmake -S . -B "$(NATIVE_BUILD_DIR)" -G Ninja -DCLAMBHOOK_ENABLE_SANITIZERS=ON
	cmake --build "$(NATIVE_BUILD_DIR)"
	ctest --test-dir "$(NATIVE_BUILD_DIR)" --output-on-failure
	cmake --build "$(NATIVE_BUILD_DIR)" --target native-license-parity

build-linux-gtk:
	cmake -S . -B "$(NATIVE_BUILD_DIR)" -G Ninja -DCLAMBHOOK_BUILD_GTK=ON
	cmake --build "$(NATIVE_BUILD_DIR)" --target clambhook-linux-c clambhook-linux-model-tests
	ctest --test-dir "$(NATIVE_BUILD_DIR)" --output-on-failure -R '^linux-gtk-'

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/clambhook "$(DESTDIR)$(PREFIX)/bin/clambhook"
	install -m 0755 bin/clambhook-tui "$(DESTDIR)$(PREFIX)/bin/clambhook-tui"
	install -m 0755 bin/clambhook-license "$(DESTDIR)$(PREFIX)/bin/clambhook-license"

install-linux: check-linux-ui-deps build-daemon build-tui build-license
	cd ui/linux && $(GRADLE) --no-daemon installDist -PclambhookDaemon="$(abspath bin/clambhook)" -PclambhookTui="$(abspath bin/clambhook-tui)" -PclambhookLicense="$(abspath bin/clambhook-license)"
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 ui/linux/build/install/clambhook-linux/bin/clambhook-linux "$(DESTDIR)$(PREFIX)/bin/clambhook-linux"
	install -d "$(DESTDIR)$(PREFIX)/lib/clambhook-linux" && cp -R ui/linux/build/install/clambhook-linux/lib/. "$(DESTDIR)$(PREFIX)/lib/clambhook-linux"
	install -d "$(DESTDIR)$(LINUX_MESON_LIBEXECDIR)"
	install -m 0755 bin/clambhook "$(DESTDIR)$(LINUX_MESON_LIBEXECDIR)/clambhook"
	install -m 0755 bin/clambhook-tui "$(DESTDIR)$(PREFIX)/bin/clambhook-tui"
	install -m 0755 bin/clambhook-license "$(DESTDIR)$(LINUX_MESON_LIBEXECDIR)/clambhook-license"
	install -d "$(DESTDIR)$(PREFIX)/share/applications"
	sed 's/@app_id@/com.clambhook.Clambhook/g' ui/linux/data/com.clambhook.Clambhook.desktop.in > "$(DESTDIR)$(PREFIX)/share/applications/com.clambhook.Clambhook.desktop"
	install -d "$(DESTDIR)$(PREFIX)/share/metainfo"
	sed 's/@app_id@/com.clambhook.Clambhook/g' ui/linux/data/com.clambhook.Clambhook.metainfo.xml.in > "$(DESTDIR)$(PREFIX)/share/metainfo/com.clambhook.Clambhook.metainfo.xml"
	install -d "$(DESTDIR)$(PREFIX)/share/icons/hicolor/1024x1024/apps"
	install -m 0644 clambhook-icon-1024.png "$(DESTDIR)$(PREFIX)/share/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png"
	install -d "$(DESTDIR)$(PREFIX)/lib/systemd/system"
	install -m 0644 packaging/systemd/clambhook-daemon.service "$(DESTDIR)$(PREFIX)/lib/systemd/system/clambhook-daemon.service"
	install -d "$(DESTDIR)$(PREFIX)/share/polkit-1/actions"
	install -m 0644 packaging/polkit/com.clambhook.Clambhook.policy "$(DESTDIR)$(PREFIX)/share/polkit-1/actions/com.clambhook.Clambhook.policy"
	install -d "$(DESTDIR)$(PREFIX)/share/doc/clambhook"
	install -m 0644 LICENSE "$(DESTDIR)$(PREFIX)/share/doc/clambhook/LICENSE"
	install -m 0644 LICENSE-APACHE "$(DESTDIR)$(PREFIX)/share/doc/clambhook/LICENSE-APACHE"
	install -m 0644 LICENSING.md "$(DESTDIR)$(PREFIX)/share/doc/clambhook/LICENSING.md"
	install -m 0644 NOTICE "$(DESTDIR)$(PREFIX)/share/doc/clambhook/NOTICE"
	install -m 0644 TRADEMARKS.md "$(DESTDIR)$(PREFIX)/share/doc/clambhook/TRADEMARKS.md"
	install -m 0644 THIRD_PARTY_NOTICES.md "$(DESTDIR)$(PREFIX)/share/doc/clambhook/THIRD_PARTY_NOTICES.md"

prepare-apple-runtime:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 $(MAKE) build-daemon
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 $(MAKE) build-tui
	./scripts/prepare-macos-runtime.sh

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: prepare-apple-runtime
	$(MAKE) generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build

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
	$(MAKE) macos-release-contract-check test lint package-smoke e2e-release

# Local mirror of GitHub Actions across Go, Apple, Android, the
# Trisquel/Rocky/Alma GNU/Linux matrix, e2e, and packaging smoke.
ci-local:
	./scripts/ci-local.sh

macos-release-contract-check:
	$(internal-release-notice)
	./scripts/macos-release-contract-check.sh

package-smoke:
	$(internal-release-notice)
	./scripts/package-smoke.sh

test-apple:
	swift test --package-path ui/apple

test-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:testDebugUnitTest

# Runs Compose instrumentation tests on Android 12, 13, and 16 (API
# 31/33/36) using Gradle build-managed AOSP devices.
test-android-compatibility:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:androidCompatibilityGroupDebugAndroidTest -Pclambhook.managedDeviceTestAbi=arm64-v8a -Pandroid.experimental.testOptions.managedDevices.maxConcurrentDevices=1 -Pandroid.testoptions.manageddevices.emulator.gpu=swiftshader_indirect

build-android-native:
	cd ui/android && $(GRADLE) --no-daemon :app:externalNativeBuildDebug

build-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assembleDebug

lint-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:lintDebug

# Build, deploy, and launch the debug app on a connected device/emulator via
# Google's `android` CLI — the default Android development tool. Requires a
# running emulator (`android emulator start <avd>`) or connected device.
run-android:
	cd ui/android && android run

build-android-release:
	$(internal-release-notice)
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assembleRelease

release-android:
	$(internal-release-notice)
	./scripts/release-android.sh

test-linux: check-linux-ui-deps
	cd ui/linux && $(GRADLE) --no-daemon test

build-linux: check-linux-ui-deps build-daemon build-tui build-license
	cd ui/linux && $(GRADLE) --no-daemon installDist -PclambhookDaemon="$(abspath bin/clambhook)" -PclambhookTui="$(abspath bin/clambhook-tui)" -PclambhookLicense="$(abspath bin/clambhook-license)"

test: build-clib
	go test ./...

test-race: build-clib
	go test -race ./...

provision-clambback-e2e:
	$(call require-command,curl,ClambBack e2e provisioning,Install curl.)
	@set -eu; \
	version="$(CLAMBBACK_E2E_VERSION)"; os="$(CLAMBBACK_E2E_OS)"; sha="$(CLAMBBACK_E2E_SHA256)"; \
	root="$(CLAMBBACK_E2E_ROOT)"; bin="$(CLAMBBACK_E2E_BIN)"; \
	if [ -x "$$bin" ]; then echo "clambback already provisioned at $$bin"; exit 0; fi; \
	archive="$(abspath bin/e2e)/clambback-$$version-$$os.tar.gz"; \
	mkdir -p "$(abspath bin/e2e)"; rm -rf "$$root"; \
	curl -fsSL --retry 3 -o "$$archive" "https://github.com/JohnThre/clambback/releases/download/v$$version/clambback-$$version-$$os.tar.gz"; \
	if command -v sha256sum >/dev/null 2>&1; then printf '%s  %s\n' "$$sha" "$$archive" | sha256sum -c -; \
	else printf '%s  %s\n' "$$sha" "$$archive" | shasum -a 256 -c -; fi; \
	tar -xzf "$$archive" -C "$(abspath bin/e2e)"; rm -f "$$archive"; \
	test -x "$$bin"; echo "provisioned clambback at $$bin"

e2e: build-daemon
	CLAMBHOOK_E2E=1 CLAMBHOOK_BIN="$(abspath bin/clambhook)" CLAMBBACK_BIN="$(E2E_CLAMBBACK_BIN)" go test -tags e2e -count=1 -v ./test/e2e

e2e-required: build-daemon provision-clambback-e2e
	CLAMBHOOK_E2E=1 CLAMBHOOK_E2E_REQUIRE_BACKENDS=1 CLAMBHOOK_BIN="$(abspath bin/clambhook)" CLAMBBACK_BIN="$(E2E_CLAMBBACK_BIN)" go test -tags e2e -count=1 -v ./test/e2e

e2e-release: build-daemon provision-clambback-e2e
	CLAMBHOOK_E2E=1 CLAMBHOOK_E2E_REQUIRE=1 CLAMBHOOK_BIN="$(abspath bin/clambhook)" CLAMBBACK_BIN="$(E2E_CLAMBBACK_BIN)" go test -tags e2e -count=1 -v ./test/e2e

e2e-tun: build-daemon provision-clambback-e2e
	mkdir -p bin/e2e
	go test -c -tags e2e -o bin/e2e/clambhook-e2e.test ./test/e2e
	@set -eu; \
	if [ "$$(id -u)" -eq 0 ]; then privilege=""; \
	elif command -v sudo >/dev/null 2>&1; then privilege="sudo"; \
	else echo "e2e-tun requires root or sudo (TUN and network namespace setup)." >&2; exit 2; fi; \
	$$privilege env CLAMBHOOK_E2E=1 CLAMBHOOK_E2E_REQUIRE=1 CLAMBHOOK_E2E_TUN=1 \
		CLAMBHOOK_BIN="$(abspath bin/clambhook)" CLAMBBACK_BIN="$(E2E_CLAMBBACK_BIN)" \
		"$(abspath bin/e2e/clambhook-e2e.test)" -test.v -test.run '^TestDaemonTUNNetNSRoundTrip$$'

lint:
	./scripts/lint.sh

clean:
	rm -rf bin/
	rm -rf "$(NATIVE_BUILD_DIR)/"
	rm -rf ui/apple/Frameworks/*.xcframework
	rm -rf ui/android/build/ ui/android/app/build/ ui/android/app/libs/
	rm -rf ui/linux/builddir/
	$(MAKE) -C clib clean
