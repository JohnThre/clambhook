# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

cmake_minimum_required(VERSION 3.22)

project(clambhook VERSION 1.0.2 LANGUAGES C)

include(GNUInstallDirs)
include(CTest)
include(TestBigEndian)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

option(CLAMBHOOK_BUILD_DAEMON "Build the native daemon" ON)
option(CLAMBHOOK_BUILD_TUI "Build the native terminal client" ON)
option(CLAMBHOOK_BUILD_LICENSE_HELPER "Build the license helper" ON)
option(CLAMBHOOK_BUILD_TESTS "Build the native unit tests" ${BUILD_TESTING})
option(CLAMBHOOK_ENABLE_SANITIZERS "Enable AddressSanitizer and UndefinedBehaviorSanitizer" OFF)
option(CLAMBHOOK_WARNINGS_AS_ERRORS "Treat first-party C warnings as errors" ON)

set(CLAMBHOOK_WARNING_FLAGS
  $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
)
if(CLAMBHOOK_WARNINGS_AS_ERRORS)
  list(APPEND CLAMBHOOK_WARNING_FLAGS
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Werror>)
endif()

find_package(PkgConfig REQUIRED)
find_package(Threads REQUIRED)
pkg_check_modules(LIBUV REQUIRED IMPORTED_TARGET libuv)
pkg_check_modules(LIBSODIUM REQUIRED IMPORTED_TARGET libsodium)
pkg_check_modules(OPENSSL REQUIRED IMPORTED_TARGET openssl>=3.0)
pkg_check_modules(CURL REQUIRED IMPORTED_TARGET libcurl)

# Pinned, unmodified MIT-licensed llhttp 9.4.3. The supported distributions do
# not provide the pinned development package, so every target uses the same
# in-tree generated parser.
set(LLHTTP_DIR ${CMAKE_CURRENT_SOURCE_DIR}/third_party/llhttp)
add_library(llhttp STATIC
  ${LLHTTP_DIR}/src/api.c
  ${LLHTTP_DIR}/src/http.c
  ${LLHTTP_DIR}/src/llhttp.c
)
target_include_directories(llhttp PUBLIC ${LLHTTP_DIR}/include)

# Pinned, unmodified C99 TOML parser. Keep third-party warnings separate from
# the project's strict warning policy; provenance lives beside the source.
add_library(tomlc99 STATIC third_party/tomlc99/toml.c)
target_include_directories(tomlc99 PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/third_party/tomlc99)
target_compile_definitions(tomlc99 PRIVATE _POSIX_C_SOURCE=200809L)

# Pinned, unmodified MIT-licensed libyaml 0.2.5 read-side parser. Outline
# dynamic access keys use YAML anchors and merge keys, so use the same frozen
# parser on every supported platform.
set(LIBYAML_DIR ${CMAKE_CURRENT_SOURCE_DIR}/third_party/libyaml)
add_library(libyaml STATIC
  ${LIBYAML_DIR}/src/api.c
  ${LIBYAML_DIR}/src/loader.c
  ${LIBYAML_DIR}/src/parser.c
  ${LIBYAML_DIR}/src/reader.c
  ${LIBYAML_DIR}/src/scanner.c
)
target_include_directories(libyaml PUBLIC ${LIBYAML_DIR}/include)
target_include_directories(libyaml PRIVATE ${LIBYAML_DIR}/src)
target_compile_definitions(libyaml PRIVATE
  _POSIX_C_SOURCE=200809L
  YAML_VERSION_STRING="0.2.5"
  YAML_VERSION_MAJOR=0
  YAML_VERSION_MINOR=2
  YAML_VERSION_PATCH=5
)

# Pinned Apache-2.0 libmaxminddb 1.13.3. The application links the static
# reader so desktop and Android builds share the frozen MMDB geo contract.
set(MAXMINDDB_DIR ${CMAKE_CURRENT_SOURCE_DIR}/third_party/libmaxminddb)
# Keep public headers strictly ISO C17 even when the compiler offers a
# non-standard 128-bit integer extension. libmaxminddb supports this portable
# byte-array representation directly.
set(MMDB_UINT128_USING_MODE 0)
set(MMDB_UINT128_IS_BYTE_ARRAY 1)
test_big_endian(CH_MMDB_BIG_ENDIAN)
configure_file(
  ${MAXMINDDB_DIR}/include/maxminddb_config.h.cmake.in
  ${CMAKE_CURRENT_BINARY_DIR}/generated/maxminddb_config.h
)
add_library(maxminddb STATIC
  ${MAXMINDDB_DIR}/src/maxminddb.c
  ${MAXMINDDB_DIR}/src/data-pool.c
)
target_include_directories(maxminddb
  PUBLIC ${MAXMINDDB_DIR}/include ${CMAKE_CURRENT_BINARY_DIR}/generated
  PRIVATE ${MAXMINDDB_DIR}/src
)
target_compile_definitions(maxminddb PRIVATE PACKAGE_VERSION="1.13.3")
if(NOT CH_MMDB_BIG_ENDIAN)
  target_compile_definitions(maxminddb PRIVATE MMDB_LITTLE_ENDIAN=1)
endif()

# Pinned BSD-3-Clause lwIP core. Clambhook uses the NO_SYS raw API on its
# serialized runtime thread and supplies a layer-3 netif in native/src.
set(LWIP_DIR ${CMAKE_CURRENT_SOURCE_DIR}/third_party/lwip)
add_library(lwipcore STATIC
  ${LWIP_DIR}/src/core/init.c
  ${LWIP_DIR}/src/core/def.c
  ${LWIP_DIR}/src/core/inet_chksum.c
  ${LWIP_DIR}/src/core/ip.c
  ${LWIP_DIR}/src/core/mem.c
  ${LWIP_DIR}/src/core/memp.c
  ${LWIP_DIR}/src/core/netif.c
  ${LWIP_DIR}/src/core/pbuf.c
  ${LWIP_DIR}/src/core/stats.c
  ${LWIP_DIR}/src/core/sys.c
  ${LWIP_DIR}/src/core/tcp.c
  ${LWIP_DIR}/src/core/tcp_in.c
  ${LWIP_DIR}/src/core/tcp_out.c
  ${LWIP_DIR}/src/core/timeouts.c
  ${LWIP_DIR}/src/core/udp.c
  ${LWIP_DIR}/src/core/ipv4/icmp.c
  ${LWIP_DIR}/src/core/ipv4/ip4.c
  ${LWIP_DIR}/src/core/ipv4/ip4_addr.c
  ${LWIP_DIR}/src/core/ipv4/ip4_frag.c
  ${LWIP_DIR}/src/core/ipv6/icmp6.c
  ${LWIP_DIR}/src/core/ipv6/inet6.c
  ${LWIP_DIR}/src/core/ipv6/ip6.c
  ${LWIP_DIR}/src/core/ipv6/ip6_addr.c
  ${LWIP_DIR}/src/core/ipv6/ip6_frag.c
  ${LWIP_DIR}/src/core/ipv6/nd6.c
)
target_include_directories(lwipcore PUBLIC
  ${LWIP_DIR}/src/include
  ${CMAKE_CURRENT_SOURCE_DIR}/native/src
)

# BSD-3-Clause WireGuard protocol core. ClambHook supplies the transport and
# lwIP netif integration; the upstream embedded adapter is intentionally not
# compiled. Provenance and the exact revision live beside the source.
set(WIREGUARD_LWIP_DIR ${CMAKE_CURRENT_SOURCE_DIR}/third_party/wireguard_lwip)
add_library(wireguard_protocol STATIC
  ${WIREGUARD_LWIP_DIR}/src/crypto.c
  ${WIREGUARD_LWIP_DIR}/src/wireguard.c
  ${WIREGUARD_LWIP_DIR}/src/crypto/refc/blake2s.c
  ${WIREGUARD_LWIP_DIR}/src/crypto/refc/chacha20.c
  ${WIREGUARD_LWIP_DIR}/src/crypto/refc/chacha20poly1305.c
  ${WIREGUARD_LWIP_DIR}/src/crypto/refc/poly1305-donna.c
  ${WIREGUARD_LWIP_DIR}/src/crypto/refc/x25519.c
)
target_include_directories(wireguard_protocol PUBLIC
  ${WIREGUARD_LWIP_DIR}/src
  ${LWIP_DIR}/src/include
  ${CMAKE_CURRENT_SOURCE_DIR}/native/src
)
target_compile_definitions(wireguard_protocol PRIVATE
  poly1305_init=ch_wg_poly1305_init
  poly1305_update=ch_wg_poly1305_update
  poly1305_finish=ch_wg_poly1305_finish
)

if(CLAMBHOOK_ENABLE_SANITIZERS AND CMAKE_C_COMPILER_ID MATCHES "AppleClang|Clang|GNU")
  add_compile_options(-fno-omit-frame-pointer -fsanitize=address,undefined)
  add_link_options(-fno-omit-frame-pointer -fsanitize=address,undefined)
endif()

add_library(clambhook_core STATIC
  native/src/conditioner.c
  native/src/config.c
  native/src/config_import.c
  native/src/config_mutation.c
  native/src/config_payload.c
  native/src/developer.c
  native/src/developer_curl.c
  native/src/dns.c
  native/src/dns_quic.c
  native/src/error.c
  native/src/events.c
  native/src/geo.c
  native/src/http_safety.c
  native/src/ip_stack.c
  native/src/json.c
  native/src/license.c
  native/src/license_http.c
  native/src/license_json.c
  native/src/listener.c
  native/src/lwip_context.c
  native/src/netwatch.c
  native/src/outline.c
  native/src/policy.c
  native/src/procattr.c
  native/src/prompt.c
  native/src/profile_converter.c
  native/src/protocol.c
  native/src/protocol_openvpn.c
  native/src/protocol_packet.c
  native/src/protocol_shadowtls.c
  native/src/protocol_shadowsocks.c
  native/src/protocol_tor.c
  native/src/protocol_vmess.c
  native/src/protocol_wireguard.c
  native/src/rule_feed.c
  native/src/rule_feed_config.c
  native/src/rules.c
  native/src/rules_config.c
  native/src/runtime.c
  native/src/runtime_config.c
  native/src/runtime_listener.c
  native/src/socks.c
  native/src/temporary_rules.c
  native/src/traffic.c
  native/src/tunnel_stack.c
  native/src/watcher.c
  native/src/wireguard_platform.c
)
add_library(Clambhook::Core ALIAS clambhook_core)

add_library(clambhook_crypto STATIC clib/src/crypto.c)
add_library(Clambhook::Crypto ALIAS clambhook_crypto)
target_include_directories(clambhook_crypto PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/clib/include>
  $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)
target_link_libraries(clambhook_crypto PUBLIC PkgConfig::OPENSSL)
target_compile_options(clambhook_crypto PRIVATE
  ${CLAMBHOOK_WARNING_FLAGS}
)

target_include_directories(clambhook_core
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/native/include>
    $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
  PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/native/src
)
target_link_libraries(clambhook_core PUBLIC
  PkgConfig::LIBUV PkgConfig::LIBSODIUM PkgConfig::CURL
  Clambhook::Crypto llhttp lwipcore maxminddb
  wireguard_protocol tomlc99 libyaml
  Threads::Threads
)
if(APPLE)
  target_link_libraries(clambhook_core PUBLIC "-framework CoreFoundation" "-framework Security")
  target_compile_definitions(clambhook_core PRIVATE _DARWIN_C_SOURCE)
elseif(UNIX)
  target_compile_definitions(clambhook_core PRIVATE _DEFAULT_SOURCE)
  target_link_libraries(clambhook_core PUBLIC m)
endif()
target_compile_definitions(clambhook_core PRIVATE _POSIX_C_SOURCE=200809L)
target_compile_options(clambhook_core PRIVATE
  ${CLAMBHOOK_WARNING_FLAGS}
)

if(CLAMBHOOK_BUILD_DAEMON)
  add_executable(clambhook native/src/api_server.c native/src/daemon/main.c)
  target_include_directories(clambhook PRIVATE native/src)
  target_link_libraries(clambhook PRIVATE Clambhook::Core)
  target_compile_definitions(clambhook PRIVATE
    CLAMBHOOK_VERSION="${PROJECT_VERSION}"
    _POSIX_C_SOURCE=200809L
  )
  target_compile_options(clambhook PRIVATE
    ${CLAMBHOOK_WARNING_FLAGS}
  )
  install(TARGETS clambhook RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
          COMPONENT Runtime)
endif()

if(CLAMBHOOK_BUILD_TUI)
  add_executable(clambhook-tui native/src/tui/main.c)
  target_link_libraries(clambhook-tui PRIVATE Clambhook::Core PkgConfig::CURL)
  target_compile_definitions(clambhook-tui PRIVATE
    CLAMBHOOK_VERSION="${PROJECT_VERSION}"
    _POSIX_C_SOURCE=200809L
  )
  target_compile_options(clambhook-tui PRIVATE
    ${CLAMBHOOK_WARNING_FLAGS}
  )
  install(TARGETS clambhook-tui RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
          COMPONENT Runtime)
endif()

if(CLAMBHOOK_BUILD_LICENSE_HELPER)
  add_executable(clambhook-license native/src/license_helper/main.c)
  target_include_directories(clambhook-license PRIVATE native/src)
  target_link_libraries(clambhook-license PRIVATE Clambhook::Core)
  target_compile_definitions(clambhook-license PRIVATE CLAMBHOOK_VERSION="${PROJECT_VERSION}")
  target_compile_options(clambhook-license PRIVATE
    ${CLAMBHOOK_WARNING_FLAGS}
  )
  add_custom_target(license-contract
    COMMAND bash ${CMAKE_CURRENT_SOURCE_DIR}/scripts/check-license-contract.sh
            $<TARGET_FILE:clambhook-license>
    DEPENDS clambhook-license
    WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}
    USES_TERMINAL
  )
  install(TARGETS clambhook-license RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
          COMPONENT Runtime)
endif()

if(CLAMBHOOK_BUILD_TESTS)
  add_executable(clambhook-native-tests
    native/src/api_server.c
    native/tests/test_api_server.c
    native/tests/test_config.c
    native/tests/test_conditioner.c
    native/tests/test_main.c
    native/tests/test_events.c
    native/tests/test_geo.c
    native/tests/test_crypto.c
    native/tests/test_developer.c
    native/tests/test_dns.c
    native/tests/test_ip_stack.c
    native/tests/test_json.c
    native/tests/test_license.c
    native/tests/test_listener.c
    native/tests/test_netwatch.c
    native/tests/test_openvpn.c
    native/tests/test_outline.c
    native/tests/test_profile_converter.c
    native/tests/test_policy.c
    native/tests/test_procattr.c
    native/tests/test_prompt.c
    native/tests/test_protocol.c
    native/tests/test_rule_feed.c
    native/tests/test_rules.c
    native/tests/test_runtime.c
    native/tests/test_runtime_listener.c
    native/tests/test_socks.c
    native/tests/test_temporary_rules.c
    native/tests/test_traffic.c
    native/tests/test_watcher.c
    native/tests/test_wireguard.c
  )
  target_include_directories(clambhook-native-tests PRIVATE native/src native/tests)
  target_compile_definitions(clambhook-native-tests PRIVATE
    CLAMBHOOK_SOURCE_DIR="${CMAKE_CURRENT_SOURCE_DIR}"
    _POSIX_C_SOURCE=200809L
  )
  if(APPLE)
    target_compile_definitions(clambhook-native-tests PRIVATE _DARWIN_C_SOURCE)
  elseif(UNIX)
    target_compile_definitions(clambhook-native-tests PRIVATE _DEFAULT_SOURCE)
  endif()
  target_link_libraries(clambhook-native-tests PRIVATE Clambhook::Core)
  add_test(NAME native-unit COMMAND clambhook-native-tests)
  if(CLAMBHOOK_BUILD_TUI)
    add_test(NAME tui-version COMMAND clambhook-tui --version)
    set_tests_properties(tui-version PROPERTIES
      PASS_REGULAR_EXPRESSION "^clambhook-tui ${PROJECT_VERSION}"
    )
    add_test(NAME tui-help COMMAND clambhook-tui --help)
    set_tests_properties(tui-help PROPERTIES
      PASS_REGULAR_EXPRESSION "layout adapts automatically to narrow terminals"
    )
  endif()
endif()

install(TARGETS clambhook_core clambhook_crypto
  EXPORT ClambhookTargets
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
  COMPONENT Development
)
install(DIRECTORY native/include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}
  COMPONENT Development)
install(FILES clib/include/cnet.h DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}
  COMPONENT Development)
install(FILES LICENSE LICENSE-APACHE LICENSING.md NOTICE TRADEMARKS.md
  THIRD_PARTY_NOTICES.md
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook
  COMPONENT Runtime)
install(FILES third_party/openssl/LICENSE.txt
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook/licenses/openssl
  COMPONENT Runtime)
install(FILES third_party/curl/LICENSE.txt
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook/licenses/curl
  COMPONENT Runtime)
install(FILES third_party/llhttp/LICENSE
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook/licenses/llhttp
  COMPONENT Runtime)
install(FILES third_party/libyaml/LICENSE
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook/licenses/libyaml
  COMPONENT Runtime)
