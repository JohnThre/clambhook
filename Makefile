.PHONY: all build build-clib build-daemon build-tui install install-linux prepare-apple-runtime build-apple-mobile-xcframework generate-apple build-apple release-macos upload-release-r2 release-check app-review-release-check package-smoke test-apple test-android build-android-mobile-aar build-android build-android-release build-android-play-release check-linux-ui-deps test-linux build-linux test e2e e2e-release lint clean

export CGO_ENABLED=1
PREFIX ?= /usr/local
LINUX_MESON_PREFIX = $(if $(PREFIX),$(PREFIX),/)
LINUX_MESON_LIBEXECDIR = $(if $(PREFIX),$(PREFIX)/libexec,/libexec)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS ?= -X main.version=$(VERSION)
ANDROID_HOME ?= $(HOME)/Library/Android/sdk

require-command = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is required for $(2)." >&2; echo "$(3)" >&2; exit 2; }
internal-release-notice = @printf '%s\n' "internal-only: this target is for developer QA/build validation and must not publish end-user installers or packages on GitHub."

all: build

check-linux-ui-deps:
	$(call require-command,meson,Linux UI targets,Install Meson and the GTK development toolchain from the Debian build dependencies.)
	$(call require-command,valac,Linux UI targets,Install Vala plus GTK4/libadwaita/gee/json-glib/libsoup 3/libsecret development packages from the Debian build dependencies.)

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

prepare-apple-runtime:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 $(MAKE) build-daemon
	./scripts/prepare-macos-runtime.sh

build-apple-mobile-xcframework:
	./scripts/build-apple-mobile-xcframework.sh

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: prepare-apple-runtime build-apple-mobile-xcframework
	$(MAKE) generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookVision -destination 'generic/platform=visionOS Simulator' CODE_SIGNING_ALLOWED=NO build

release-macos:
	$(internal-release-notice)
	./scripts/release-macos.sh

upload-release-r2:
	$(internal-release-notice)
	./scripts/upload-release-r2.sh

release-check:
	$(internal-release-notice)
	$(MAKE) test lint package-smoke e2e-release

app-review-release-check:
	$(internal-release-notice)
	./scripts/app-review-compliance-check.sh --require-demo-secret

package-smoke:
	$(internal-release-notice)
	./scripts/package-smoke.sh

test-apple:
	swift test --package-path ui/apple

test-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:testPlayDebugUnitTest

build-android-mobile-aar:
	./scripts/build-android-mobile-aar.sh

build-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assemblePlayDebug

build-android-release:
	$(internal-release-notice)
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assemblePlayRelease

build-android-play-release:
	$(internal-release-notice)
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assemblePlayRelease

test-linux: check-linux-ui-deps
	cd ui/linux && meson setup builddir --reconfigure && meson test -C builddir

build-linux: check-linux-ui-deps build-daemon
	cd ui/linux && meson setup builddir --reconfigure -Dclambhook_daemon="$(abspath bin/clambhook)" && meson compile -C builddir

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
