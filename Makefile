.PHONY: all build build-clib build-daemon build-tui prepare-apple-runtime generate-apple build-apple release-macos test-apple test-android build-android test-windows build-windows-daemon build-windows publish-windows test-linux build-linux build-linux-flatpak test-linux-flatpak test lint clean

export CGO_ENABLED=1
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
DOTNET ?= dotnet
WINDOWS_RID ?= win-x64
WINDOWS_GOARCH_win-x64 := amd64
WINDOWS_GOARCH_win-arm64 := arm64
WINDOWS_GOARCH := $(WINDOWS_GOARCH_$(WINDOWS_RID))
WINDOWS_DAEMON := bin/windows/$(WINDOWS_RID)/clambhook.exe
LINUX_FLATPAK_MANIFEST := ui/linux/com.clambhook.Clambhook.yml
LINUX_FLATPAK_BUILD_DIR := dist/linux/build
LINUX_FLATPAK_REPO := dist/linux/repo
LINUX_FLATPAK_BUNDLE := dist/linux/com.clambhook.Clambhook.flatpak

all: build

build-clib:
	$(MAKE) -C clib

build-daemon: build-clib
	mkdir -p bin
	go build -o bin/clambhook ./cmd/clambhook

build-tui: build-clib
	mkdir -p bin
	go build -o bin/clambhook-tui ./cmd/clambhook-tui

build: build-daemon build-tui

prepare-apple-runtime: build-daemon
	./scripts/prepare-macos-runtime.sh

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: prepare-apple-runtime generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookiOS -destination 'generic/platform=iOS Simulator' CODE_SIGNING_ALLOWED=NO build

release-macos:
	./scripts/release-macos.sh

test-apple:
	swift test --package-path ui/apple

test-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:testDebugUnitTest

build-android:
	cd ui/android && ANDROID_HOME="$(ANDROID_HOME)" ./gradlew :app:assembleDebug

test-windows:
	$(DOTNET) test ui/windows/Clambhook.Windows.sln

build-windows-daemon:
	@if [ -z "$(WINDOWS_GOARCH)" ]; then echo "unsupported WINDOWS_RID=$(WINDOWS_RID) (expected win-x64 or win-arm64)" >&2; exit 2; fi
	mkdir -p bin/windows/$(WINDOWS_RID)
	CGO_ENABLED=0 GOOS=windows GOARCH=$(WINDOWS_GOARCH) go build -o $(WINDOWS_DAEMON) ./cmd/clambhook

build-windows: build-windows-daemon
	$(DOTNET) build ui/windows/src/Clambhook.Windows/Clambhook.Windows.csproj -c Debug -r $(WINDOWS_RID) -p:ClambhookDaemonPath="$(abspath $(WINDOWS_DAEMON))"

publish-windows: build-windows-daemon
	$(DOTNET) publish ui/windows/src/Clambhook.Windows/Clambhook.Windows.csproj -c Release -r $(WINDOWS_RID) --self-contained true -p:ClambhookDaemonPath="$(abspath $(WINDOWS_DAEMON))"

test-linux:
	cd ui/linux && meson setup builddir --reconfigure && meson test -C builddir

build-linux: build-daemon
	cd ui/linux && meson setup builddir --reconfigure && meson compile -C builddir

build-linux-flatpak:
	@command -v flatpak-builder >/dev/null || { echo "flatpak-builder is required. Install flatpak-builder and configure the Flathub remote." >&2; exit 127; }
	@command -v flatpak >/dev/null || { echo "flatpak is required. Install flatpak and configure the Flathub remote." >&2; exit 127; }
	mkdir -p dist/linux
	flatpak-builder --force-clean --install-deps-from=flathub --repo=$(LINUX_FLATPAK_REPO) $(LINUX_FLATPAK_BUILD_DIR) $(LINUX_FLATPAK_MANIFEST)
	flatpak build-bundle $(LINUX_FLATPAK_REPO) $(LINUX_FLATPAK_BUNDLE) com.clambhook.Clambhook
	@echo "Created $(LINUX_FLATPAK_BUNDLE)"

test-linux-flatpak: build-linux-flatpak
	test -x $(LINUX_FLATPAK_BUILD_DIR)/files/bin/clambhook-linux
	test -x $(LINUX_FLATPAK_BUILD_DIR)/files/libexec/clambhook
	flatpak build $(LINUX_FLATPAK_BUILD_DIR) /app/libexec/clambhook -version

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
	rm -rf ui/android/build/ ui/android/app/build/
	rm -rf ui/linux/builddir/
	find ui/windows -type d \( -name bin -o -name obj \) -prune -exec rm -rf {} +
	$(MAKE) -C clib clean
