.PHONY: all build build-clib build-daemon build-tui test lint clean

export CGO_ENABLED=1

all: build

build-clib:
	$(MAKE) -C clib

build-daemon: build-clib
	go build -o bin/clambhook ./cmd/clambhook

build-tui: build-clib
	go build -o bin/clambhook-tui ./cmd/clambhook-tui

build: build-daemon build-tui

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
	$(MAKE) -C clib clean
