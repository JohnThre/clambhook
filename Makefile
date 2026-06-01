.PHONY: all build build-clib build-daemon build-tui install install-linux prepare-apple-runtime generate-apple build-apple archive-iphone build-iphone release-macos release-check package-smoke test-apple test-android build-android-mobile-aar build-ios-mobile-xcframework build-android build-android-release build-android-play-release check-linux-ui-deps check-linux-flatpak-deps test-linux build-linux build-linux-flatpak test-linux-flatpak test e2e e2e-release lint clean

export CGO_ENABLED=1
PREFIX ?= /usr/local
LINUX_MESON_PREFIX = $(if $(PREFIX),$(PREFIX),/)
LINUX_MESON_LIBEXECDIR = $(if $(PREFIX),$(PREFIX)/libexec,/libexec)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS ?= -X main.version=$(VERSION)
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
LINUX_FLATPAK_MANIFEST := ui/linux/com.clambhook.Clambhook.yml
LINUX_FLATPAK_BUILD_DIR := dist/linux/build
LINUX_FLATPAK_REPO := dist/linux/repo
LINUX_FLATPAK_BUNDLE := dist/linux/com.clambhook.Clambhook.flatpak

require-command = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is required for $(2)." >&2; echo "$(3)" >&2; exit 2; }

all: build

check-linux-ui-deps:
	$(call require-command,meson,Linux UI targets,Install Meson and the GTK development toolchain for your distribution.)
	$(call require-command,valac,Linux UI targets,Install Vala plus GTK4/libadwaita/gee/json-glib/libsoup 3/libsecret development packages.)

check-linux-flatpak-deps:
	$(call require-command,flatpak-builder,Linux Flatpak targets,Install flatpak-builder and configure the Flathub remote.)
	$(call require-command,flatpak,Linux Flatpak targets,Install flatpak and configure the Flathub remote.)

build-clib:
	$(MAKE) -C clib

build-daemon: build-clib
	mkdir -p bin
	go build -ldflags "$(GO_LDFLAGS)" -o bin/clambhook ./cmd/clambhook

build-tui: build-clib
	mkdir -p bin
	go build -ldflags "$(GO_LDFLAGS)" -o bin/clambhook-tui ./cmd/clambhook-tui

build: build-daemon build-tui

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/clambhook "$(DESTDIR)$(PREFIX)/bin/clambhook"
	install -m 0755 bin/clambhook-tui "$(DESTDIR)$(PREFIX)/bin/clambhook-tui"

install-linux: check-linux-ui-deps build-daemon
	cd ui/linux && meson setup builddir --prefix="$(LINUX_MESON_PREFIX)" --libexecdir="$(LINUX_MESON_LIBEXECDIR)" --reconfigure -Dclambhook_daemon="$(abspath bin/clambhook)"
	cd ui/linux && meson install -C builddir $(if $(DESTDIR),--destdir "$(abspath $(DESTDIR))",)

prepare-apple-runtime: build-daemon
	./scripts/prepare-macos-runtime.sh

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: prepare-apple-runtime build-ios-mobile-xcframework
	$(MAKE) generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookiOS -destination 'generic/platform=iOS Simulator' CODE_SIGNING_ALLOWED=NO build
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookVision -destination 'generic/platform=visionOS Simulator' CODE_SIGNING_ALLOWED=NO build

archive-iphone:
	./scripts/archive-ios-app-store.sh

build-iphone: archive-iphone

release-macos:
	./scripts/release-macos.sh

release-check: test lint package-smoke e2e-release

package-smoke:
	./scripts/package-smoke.sh

test-apple:
	swift test --package-path ui/apple

test-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:testPlayDebugUnitTest

build-android-mobile-aar:
	./scripts/build-android-mobile-aar.sh

build-ios-mobile-xcframework:
	./scripts/build-ios-mobile-xcframework.sh

build-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assemblePlayDebug

build-android-release:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assemblePlayRelease

build-android-play-release:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assemblePlayRelease

test-linux: check-linux-ui-deps
	cd ui/linux && meson setup builddir --reconfigure && meson test -C builddir

build-linux: check-linux-ui-deps build-daemon
	cd ui/linux && meson setup builddir --reconfigure -Dclambhook_daemon="$(abspath bin/clambhook)" && meson compile -C builddir

build-linux-flatpak: check-linux-flatpak-deps
	mkdir -p dist/linux
	flatpak-builder --force-clean --install-deps-from=flathub --repo=$(LINUX_FLATPAK_REPO) $(LINUX_FLATPAK_BUILD_DIR) $(LINUX_FLATPAK_MANIFEST)
	flatpak build-bundle $(LINUX_FLATPAK_REPO) $(LINUX_FLATPAK_BUNDLE) com.clambhook.Clambhook
	@echo "Created $(LINUX_FLATPAK_BUNDLE)"

test-linux-flatpak: build-linux-flatpak
	test -x $(LINUX_FLATPAK_BUILD_DIR)/files/bin/clambhook-linux
	test -x $(LINUX_FLATPAK_BUILD_DIR)/files/libexec/clambhook
	test -f $(LINUX_FLATPAK_BUILD_DIR)/files/share/applications/com.clambhook.Clambhook.desktop
	test -f $(LINUX_FLATPAK_BUILD_DIR)/files/share/metainfo/com.clambhook.Clambhook.metainfo.xml
	test -f $(LINUX_FLATPAK_BUILD_DIR)/files/share/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png
	flatpak build $(LINUX_FLATPAK_BUILD_DIR) /app/libexec/clambhook -version

test:
	go test ./...

e2e: build-daemon
	CLAMBHOOK_E2E=1 CLAMBHOOK_BIN="$(abspath bin/clambhook)" go test -tags e2e -count=1 -v ./test/e2e

e2e-release: build-daemon
	CLAMBHOOK_E2E=1 CLAMBHOOK_E2E_REQUIRE=1 CLAMBHOOK_BIN="$(abspath bin/clambhook)" go test -tags e2e -count=1 -v ./test/e2e

lint:
	./scripts/lint.sh

clean:
	rm -rf bin/
	rm -rf ui/apple/Frameworks/*.xcframework
	rm -rf ui/android/build/ ui/android/app/build/ ui/android/app/libs/
	rm -rf ui/linux/builddir/
	$(MAKE) -C clib clean
