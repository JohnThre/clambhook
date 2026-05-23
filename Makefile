.PHONY: all build build-clib build-daemon build-tui install prepare-apple-runtime generate-apple build-apple release-macos release-check package-smoke test-apple test-android build-android-mobile-aar test-android build-android build-android-release check-windows-host test-windows build-windows-daemon build-windows publish-windows check-linux-ui-deps test-linux build-linux test e2e e2e-release lint clean

export CGO_ENABLED=1
PREFIX ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS ?= -X main.version=$(VERSION)
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
DOTNET ?= dotnet
WINDOWS_RID ?= win-x64
WINDOWS_GOARCH_win-x64 := amd64
WINDOWS_GOARCH_win-arm64 := arm64
WINDOWS_GOARCH := $(WINDOWS_GOARCH_$(WINDOWS_RID))
WINDOWS_DAEMON := bin/windows/$(WINDOWS_RID)/clambhook.exe

require-command = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is required for $(2)." >&2; echo "$(3)" >&2; exit 2; }
require-windows-host = @if [ "$(OS)" != "Windows_NT" ]; then echo "test-windows must run on Windows; current host is $$(uname -s 2>/dev/null || echo unknown)." >&2; echo "The Windows tests target net10.0-windows and WinUI. Use a Windows host or CI runner." >&2; exit 2; fi

all: build

check-windows-host:
	$(require-windows-host)

check-linux-ui-deps:
	$(call require-command,meson,Linux UI targets,Install Meson and the GTK development toolchain for your distribution.)
	$(call require-command,valac,Linux UI targets,Install Vala plus GTK4/libadwaita/gee/json-glib/libsoup 3/libsecret development packages.)

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

prepare-apple-runtime: build-daemon
	./scripts/prepare-macos-runtime.sh

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: prepare-apple-runtime generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookiOS -destination 'generic/platform=iOS Simulator' CODE_SIGNING_ALLOWED=NO build

release-macos:
	./scripts/release-macos.sh

release-check: test lint package-smoke e2e-release

package-smoke:
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
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assembleRelease

test-windows: check-windows-host
	$(DOTNET) test ui/windows/Clambhook.Windows.sln

build-windows-daemon:
	@if [ -z "$(WINDOWS_GOARCH)" ]; then echo "unsupported WINDOWS_RID=$(WINDOWS_RID) (expected win-x64 or win-arm64)" >&2; exit 2; fi
	mkdir -p bin/windows/$(WINDOWS_RID)
	CGO_ENABLED=0 GOOS=windows GOARCH=$(WINDOWS_GOARCH) go build -o $(WINDOWS_DAEMON) ./cmd/clambhook

build-windows: build-windows-daemon
	$(DOTNET) build ui/windows/src/Clambhook.Windows/Clambhook.Windows.csproj -c Debug -r $(WINDOWS_RID) -p:ClambhookDaemonPath="$(abspath $(WINDOWS_DAEMON))"

publish-windows: build-windows-daemon
	$(DOTNET) publish ui/windows/src/Clambhook.Windows/Clambhook.Windows.csproj -c Release -r $(WINDOWS_RID) --self-contained true -p:ClambhookDaemonPath="$(abspath $(WINDOWS_DAEMON))"

test-linux: check-linux-ui-deps
	cd ui/linux && meson setup builddir --reconfigure && meson test -C builddir

build-linux: check-linux-ui-deps build-daemon
	cd ui/linux && meson setup builddir --reconfigure && meson compile -C builddir

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
	rm -rf ui/android/build/ ui/android/app/build/ ui/android/app/libs/
	rm -rf ui/linux/builddir/
	find ui/windows -type d \( -name bin -o -name obj \) -prune -exec rm -rf {} +
	$(MAKE) -C clib clean
