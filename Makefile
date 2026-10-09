# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

.PHONY: all build build-clib build-daemon build-license build-native \
	test-native install install-linux release-linux release-check ci-local \
	package-smoke test lint clean

PREFIX ?= /usr/local
DESTDIR ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
NATIVE_BUILD_DIR ?= build-native
NATIVE_SANITIZE_DIR ?= build-native-sanitize
internal-release-notice = @printf '%s\n' "local build: publish only through scripts/publish-release.sh after signature verification."

all: build

build-clib:
	$(MAKE) -C clib

build-native:
	cmake -S . -B "$(NATIVE_BUILD_DIR)" -G Ninja \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo -DCLAMBHOOK_ENABLE_SANITIZERS=OFF
	cmake --build "$(NATIVE_BUILD_DIR)"

build-daemon: build-native
	@test -x "$(NATIVE_BUILD_DIR)/clambhook"

build-license: build-native
	@test -x "$(NATIVE_BUILD_DIR)/clambhook-license"

build: build-native

test-native:
	cmake -S . -B "$(NATIVE_SANITIZE_DIR)" -G Ninja \
		-DCMAKE_BUILD_TYPE=Debug -DCLAMBHOOK_ENABLE_SANITIZERS=ON \
		-DCLAMBHOOK_ENABLE_MEMORY_TESTING=ON
	cmake --build "$(NATIVE_SANITIZE_DIR)"
	ctest --test-dir "$(NATIVE_SANITIZE_DIR)" --output-on-failure
	cmake --build "$(NATIVE_SANITIZE_DIR)" --target license-contract

install: build-native
	DESTDIR="$(DESTDIR)" cmake --install "$(NATIVE_BUILD_DIR)" --prefix "$(PREFIX)" --component Runtime

# Core package payload: daemon, license helper, docs, systemd unit,
# sysusers/tmpfiles and repository configuration are installed by the
# package recipes; the proprietary clambhook-ui package adds the clients.
install-linux: install
	install -d "$(DESTDIR)$(PREFIX)/lib/systemd/system"
	install -m 0644 packaging/systemd/clambhook-daemon.service "$(DESTDIR)$(PREFIX)/lib/systemd/system/clambhook-daemon.service"

release-linux:
	$(internal-release-notice)
	./scripts/release-linux.sh

release-check:
	$(internal-release-notice)
	$(MAKE) test lint package-smoke

ci-local:
	./scripts/ci-local.sh

package-smoke:
	$(internal-release-notice)
	./scripts/package-smoke.sh

test: test-native

lint:
	./scripts/lint.sh

clean:
	rm -rf bin/ "$(NATIVE_BUILD_DIR)/" "$(NATIVE_SANITIZE_DIR)/"
	$(MAKE) -C clib clean
