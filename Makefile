# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

.PHONY: all build build-clib build-daemon build-tui build-license build-native \
	test-native test-javafx test-android test-android-compatibility test-linux \
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
MAVEN ?= mvn
JAVAFX_TEST_GOALS ?= clean verify
CLAMBHOOK_HOST_OS ?= $(shell uname -s)
GLUON_LINUX_ARCH ?= $(shell uname -m)
GLUON_LINUX_TARGET = $(if $(filter arm64 aarch64,$(GLUON_LINUX_ARCH)),aarch64-linux,x86_64-linux)
GLUON_LINUX_BINARY ?= ui/javafx/target/gluonfx/$(GLUON_LINUX_TARGET)/clambhook-ui
GLUON_JAVAFX_STATIC_VERSION ?= 21.0.1
GLUON_ARM64_BUILD_DIR ?= $(CURDIR)/build-gluon-linux-aarch64
GLUON_ARM64_MAVEN_REPO ?= $(GLUON_ARM64_BUILD_DIR)/m2
GLUON_ARM64_JAVAFX_SDK ?= $(GLUON_ARM64_BUILD_DIR)/javafx-static-sdk

ifeq ($(GLUON_LINUX_TARGET),aarch64-linux)
GLUON_LINUX_MAVEN = JAVAFX_STATIC_SDK_PATH="$(GLUON_ARM64_JAVAFX_SDK)" \
	$(MAVEN) -Dmaven.repo.local="$(GLUON_ARM64_MAVEN_REPO)"
else
GLUON_LINUX_MAVEN = $(MAVEN)
endif

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
		-DCMAKE_BUILD_TYPE=Debug -DCLAMBHOOK_ENABLE_SANITIZERS=ON
	cmake --build "$(NATIVE_SANITIZE_DIR)"
	ctest --test-dir "$(NATIVE_SANITIZE_DIR)" --output-on-failure
	cmake --build "$(NATIVE_SANITIZE_DIR)" --target license-contract

test-javafx:
	@if [ "$(CLAMBHOOK_HOST_OS)" = "Linux" ]; then \
		command -v timeout >/dev/null 2>&1 || { \
			echo "timeout is required for GNU/Linux JavaFX tests." >&2; \
			exit 2; \
		}; \
		if [ -z "$${DISPLAY:-}" ]; then \
			command -v xvfb-run >/dev/null 2>&1 || { \
				echo "xvfb-run is required for headless GNU/Linux JavaFX tests." >&2; \
				exit 2; \
			}; \
			cd ui/javafx && timeout --kill-after=15s 10m xvfb-run -a $(MAVEN) -B $(JAVAFX_TEST_GOALS); \
		else \
			cd ui/javafx && timeout --kill-after=15s 10m $(MAVEN) -B $(JAVAFX_TEST_GOALS); \
		fi; \
	else \
		cd ui/javafx && $(MAVEN) -B $(JAVAFX_TEST_GOALS); \
	fi

test-linux: test-javafx

check-linux-ui-deps:
	@test "$$(uname -s)" = "Linux" || { echo "GNU/Linux is required for the Gluon desktop target." >&2; exit 2; }
	$(call require-command,$(MAVEN),GNU/Linux JavaFX targets,Install Maven 3.9+.)
	@test -n "$${GRAALVM_HOME:-}" || { echo "GRAALVM_HOME must point to GraalVM for JDK 17." >&2; exit 2; }

build-linux: check-linux-ui-deps
	@if [ "$(GLUON_LINUX_TARGET)" = "aarch64-linux" ]; then \
		scripts/prepare-gluon-linux-aarch64.sh \
			"$(GLUON_JAVAFX_STATIC_VERSION)" "$(GLUON_ARM64_BUILD_DIR)"; \
	fi
	cd ui/javafx && $(GLUON_LINUX_MAVEN) -B -Pdesktop gluonfx:compile
	cd ui/javafx && $(GLUON_LINUX_MAVEN) -B -Pdesktop gluonfx:link

build-linux-package: build-linux
	cd ui/javafx && $(MAVEN) -B -Pdesktop gluonfx:package

install: build-native
	DESTDIR="$(DESTDIR)" cmake --install "$(NATIVE_BUILD_DIR)" --prefix "$(PREFIX)" --component Runtime

install-linux: install
	@test -x "$(GLUON_LINUX_BINARY)" || $(MAKE) build-linux
	@test -x "$(GLUON_LINUX_BINARY)" || { echo "Gluon image not found: $(GLUON_LINUX_BINARY)" >&2; exit 2; }
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 "$(GLUON_LINUX_BINARY)" "$(DESTDIR)$(PREFIX)/bin/clambhook-ui"
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
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew --no-daemon :platform:assembleRelease

build-android-native:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew --no-daemon :platform:externalNativeBuildDebug

test-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew --no-daemon :platform:testDebugUnitTest :platform:lintDebug :platform:assembleRelease

test-android-compatibility:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew --no-daemon \
		:platform:androidCompatibilityGroupDebugAndroidTest \
		-Pandroid.experimental.testOptions.managedDevices.maxConcurrentDevices=1

build-android: build-android-platform
	@test -n "$${GRAALVM_HOME:-}" || { echo "GRAALVM_HOME must point to GraalVM for JDK 17." >&2; exit 2; }
	@test -d "$(ANDROID_SDK)" || { echo "ANDROID_SDK does not exist: $(ANDROID_SDK)" >&2; exit 2; }
	@test -d "$(ANDROID_NDK)" || { echo "ANDROID_NDK does not exist: $(ANDROID_NDK)" >&2; exit 2; }
	bash scripts/prepare-gluon-android.sh
	cd ui/javafx && ANDROID_SDK="$(ANDROID_SDK)" ANDROID_NDK="$(ANDROID_NDK)" \
		$(MAVEN) -B -Pandroid gluonfx:build

lint-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew --no-daemon :platform:lintDebug

run-android:
	cd ui/android && android run

build-android-release: build-android
	$(internal-release-notice)
	cd ui/javafx && $(MAVEN) -B -Pandroid gluonfx:package

release-android:
	$(internal-release-notice)
	./scripts/release-android.sh

test: test-native test-javafx test-android

lint:
	./scripts/lint.sh

clean:
	rm -rf bin/ "$(NATIVE_BUILD_DIR)/" "$(NATIVE_SANITIZE_DIR)/" \
		"$(GLUON_ARM64_BUILD_DIR)/"
	rm -rf ui/apple/Frameworks/*.xcframework
	rm -rf ui/android/build/ ui/android/app/build/ ui/android/app/libs/
	rm -rf ui/javafx/target/
	$(MAKE) -C clib clean
