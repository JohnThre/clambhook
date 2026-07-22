.PHONY: all build build-clib build-daemon build-tui build-license install install-linux prepare-apple-runtime generate-apple build-apple check-macos-signing release-macos release-linux upload-release-r2 release-check macos-release-contract-check package-smoke test-apple test-android build-android-mobile-aar build-android build-android-release release-android upload-release-android check-linux-ui-deps test-linux build-linux test test-race provision-clambback-e2e e2e e2e-required e2e-release e2e-tun lint clean

export CGO_ENABLED=1
PREFIX ?= /usr/local
LINUX_MESON_PREFIX = $(if $(PREFIX),$(PREFIX),/)
LINUX_MESON_LIBEXECDIR = $(if $(PREFIX),$(PREFIX)/libexec,/libexec)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS ?= -X main.version=$(VERSION)
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
CLAMBBACK_E2E_VERSION ?= 1.0.0-alpha.3
CLAMBBACK_E2E_OS := $(if $(filter Darwin,$(shell uname -s)),Darwin,Linux)
CLAMBBACK_E2E_SHA256_Linux := 8ec796c909c73d3ad686d53d5b3f6e104d15ce6a0c2514a01622d91e3b46758c
CLAMBBACK_E2E_SHA256_Darwin := 0578e71380ce28f43b983f7f2388c8e13b8424bec8c375774101f4dce405ca27
CLAMBBACK_E2E_SHA256 ?= $(CLAMBBACK_E2E_SHA256_$(CLAMBBACK_E2E_OS))
CLAMBBACK_E2E_BINPATH_Linux := usr/bin/clambback
CLAMBBACK_E2E_BINPATH_Darwin := usr/local/bin/clambback
CLAMBBACK_E2E_ROOT := $(abspath bin/e2e/clambback-$(CLAMBBACK_E2E_VERSION)-$(CLAMBBACK_E2E_OS))
CLAMBBACK_E2E_BIN := $(CLAMBBACK_E2E_ROOT)/$(CLAMBBACK_E2E_BINPATH_$(CLAMBBACK_E2E_OS))
E2E_CLAMBBACK_BIN = $(if $(CLAMBBACK_BIN),$(abspath $(CLAMBBACK_BIN)),$(CLAMBBACK_E2E_BIN))

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

build-license:
	mkdir -p bin
	go build -ldflags "$(GO_LDFLAGS)" -o bin/clambhook-license ./cmd/clambhook-license

build: build-daemon build-tui build-license

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/clambhook "$(DESTDIR)$(PREFIX)/bin/clambhook"
	install -m 0755 bin/clambhook-tui "$(DESTDIR)$(PREFIX)/bin/clambhook-tui"
	install -m 0755 bin/clambhook-license "$(DESTDIR)$(PREFIX)/bin/clambhook-license"

install-linux: check-linux-ui-deps build-daemon build-tui build-license
	cd ui/linux && meson setup builddir --prefix="$(LINUX_MESON_PREFIX)" --libexecdir="$(LINUX_MESON_LIBEXECDIR)" --reconfigure -Dclambhook_daemon="$(abspath bin/clambhook)" -Dclambhook_tui="$(abspath bin/clambhook-tui)" -Dclambhook_license="$(abspath bin/clambhook-license)"
	cd ui/linux && meson install -C builddir $(if $(DESTDIR),--destdir "$(abspath $(DESTDIR))",)

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

upload-release-r2:
	$(internal-release-notice)
	./scripts/upload-release-r2.sh

upload-release-linux:
	$(internal-release-notice)
	./scripts/upload-release-linux.sh

release-check:
	$(internal-release-notice)
	$(MAKE) macos-release-contract-check test lint package-smoke e2e-release

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

build-android-mobile-aar:
	./scripts/build-android-mobile-aar.sh

build-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assembleDebug

build-android-release:
	$(internal-release-notice)
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assembleRelease

release-android:
	$(internal-release-notice)
	./scripts/release-android.sh

upload-release-android:
	$(internal-release-notice)
	./scripts/upload-release-android.sh

test-linux: check-linux-ui-deps
	cd ui/linux && meson setup builddir --reconfigure && meson test -C builddir

build-linux: check-linux-ui-deps build-daemon build-tui build-license
	cd ui/linux && rm -rf builddir && meson setup builddir -Dclambhook_daemon="$(abspath bin/clambhook)" -Dclambhook_tui="$(abspath bin/clambhook-tui)" -Dclambhook_license="$(abspath bin/clambhook-license)" && meson compile -C builddir

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
	rm -rf ui/apple/Frameworks/*.xcframework
	rm -rf ui/android/build/ ui/android/app/build/ ui/android/app/libs/
	rm -rf ui/linux/builddir/
	$(MAKE) -C clib clean
