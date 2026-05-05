.PHONY: all build build-clib build-daemon build-tui generate-apple build-apple test-apple test lint clean

export CGO_ENABLED=1

all: build

build-clib:
	$(MAKE) -C clib

build-daemon: build-clib
	go build -o bin/clambhook ./cmd/clambhook

build-tui: build-clib
	go build -o bin/clambhook-tui ./cmd/clambhook-tui

build: build-daemon build-tui

generate-apple:
	cd ui/apple && xcodegen generate --spec project.yml

build-apple: build-daemon generate-apple
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
	xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookiOS -destination 'generic/platform=iOS Simulator' CODE_SIGNING_ALLOWED=NO build

test-apple:
	swift test --package-path ui/apple

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
	$(MAKE) -C clib clean
