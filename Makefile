.PHONY: all build build-clib build-daemon build-tui prepare-apple-runtime generate-apple build-apple release-macos test-apple test-android build-android test-windows build-windows test-linux build-linux test lint clean

export CGO_ENABLED=1
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
DOTNET ?= dotnet

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

build-windows:
	$(DOTNET) build ui/windows/Clambhook.Windows.sln -c Debug

test-linux:
	cd ui/linux && meson setup builddir --reconfigure && meson test -C builddir

build-linux: build-daemon
	cd ui/linux && meson setup builddir --reconfigure && meson compile -C builddir

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
