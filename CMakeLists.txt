# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

cmake_minimum_required(VERSION 3.22)

project(clambhook VERSION 1.0.2 LANGUAGES C)

include(GNUInstallDirs)
include(CTest)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

option(CLAMBHOOK_BUILD_DAEMON "Build the native daemon" ON)
option(CLAMBHOOK_BUILD_TUI "Build the native terminal client" ON)
option(CLAMBHOOK_BUILD_LICENSE_HELPER "Build the native license helper migration binary" ON)
option(CLAMBHOOK_BUILD_GTK "Build the experimental GTK 4 GNU/Linux client" OFF)
option(CLAMBHOOK_BUILD_JNI "Build the experimental JNI runtime bridge" OFF)
option(CLAMBHOOK_BUILD_TESTS "Build the native unit tests" ${BUILD_TESTING})
option(CLAMBHOOK_ENABLE_SANITIZERS "Enable AddressSanitizer and UndefinedBehaviorSanitizer" OFF)

find_package(PkgConfig REQUIRED)
find_package(Threads REQUIRED)
pkg_check_modules(LIBUV REQUIRED IMPORTED_TARGET libuv)
pkg_check_modules(LIBSODIUM REQUIRED IMPORTED_TARGET libsodium)
pkg_check_modules(LLHTTP REQUIRED IMPORTED_TARGET libllhttp)
pkg_check_modules(OPENSSL REQUIRED IMPORTED_TARGET openssl>=3.0)
pkg_check_modules(CURL REQUIRED IMPORTED_TARGET libcurl)

# Pinned, unmodified C99 TOML parser. Keep third-party warnings separate from
# the project's strict warning policy; provenance lives beside the source.
add_library(tomlc99 STATIC third_party/tomlc99/toml.c)
target_include_directories(tomlc99 PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/third_party/tomlc99)
target_compile_definitions(tomlc99 PRIVATE _POSIX_C_SOURCE=200809L)

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
  native/src/http_safety.c
  native/src/ip_stack.c
  native/src/json.c
  native/src/license.c
  native/src/license_http.c
  native/src/license_json.c
  native/src/listener.c
  native/src/lwip_context.c
  native/src/netwatch.c
  native/src/policy.c
  native/src/procattr.c
  native/src/prompt.c
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
  $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
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
  Clambhook::Crypto lwipcore wireguard_protocol tomlc99 Threads::Threads
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
  $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
)

if(CLAMBHOOK_BUILD_DAEMON)
  add_executable(clambhook-c native/src/api_server.c native/src/daemon/main.c)
  target_include_directories(clambhook-c PRIVATE native/src)
  target_link_libraries(clambhook-c PRIVATE Clambhook::Core PkgConfig::LLHTTP)
  target_compile_definitions(clambhook-c PRIVATE
    CLAMBHOOK_VERSION="${PROJECT_VERSION}"
    _POSIX_C_SOURCE=200809L
  )
  set_target_properties(clambhook-c PROPERTIES OUTPUT_NAME clambhook-c)
endif()

if(CLAMBHOOK_BUILD_TUI)
  add_executable(clambhook-tui-c native/src/tui/main.c)
  target_link_libraries(clambhook-tui-c PRIVATE Clambhook::Core PkgConfig::CURL)
  target_compile_definitions(clambhook-tui-c PRIVATE
    CLAMBHOOK_VERSION="${PROJECT_VERSION}"
    _POSIX_C_SOURCE=200809L
  )
  target_compile_options(clambhook-tui-c PRIVATE
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
  )
endif()

if(CLAMBHOOK_BUILD_LICENSE_HELPER)
  add_executable(clambhook-license-c native/src/license_helper/main.c)
  target_include_directories(clambhook-license-c PRIVATE native/src)
  target_link_libraries(clambhook-license-c PRIVATE Clambhook::Core)
  target_compile_definitions(clambhook-license-c PRIVATE CLAMBHOOK_VERSION="${PROJECT_VERSION}")
  target_compile_options(clambhook-license-c PRIVATE
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
  )
  add_custom_target(native-license-parity
    COMMAND bash ${CMAKE_CURRENT_SOURCE_DIR}/scripts/check-native-license-parity.sh
            $<TARGET_FILE:clambhook-license-c>
    DEPENDS clambhook-license-c
    WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}
    USES_TERMINAL
  )
endif()

if(CLAMBHOOK_BUILD_GTK)
  pkg_check_modules(GTK4 REQUIRED IMPORTED_TARGET gtk4)
  pkg_check_modules(LIBSOUP REQUIRED IMPORTED_TARGET libsoup-3.0)
  pkg_check_modules(JSON_GLIB REQUIRED IMPORTED_TARGET json-glib-1.0)
  add_library(clambhook-linux-model STATIC ui/linux-gtk/src/model.c)
  target_include_directories(clambhook-linux-model PUBLIC ui/linux-gtk/src)
  target_link_libraries(clambhook-linux-model PUBLIC PkgConfig::JSON_GLIB)
  target_compile_options(clambhook-linux-model PRIVATE
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
  )
  add_executable(clambhook-linux-c ui/linux-gtk/src/main.c)
  target_link_libraries(clambhook-linux-c PRIVATE
    clambhook-linux-model Clambhook::Core PkgConfig::GTK4 PkgConfig::LIBSOUP
  )
  target_compile_options(clambhook-linux-c PRIVATE
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
  )
  target_compile_definitions(clambhook-linux-c PRIVATE
    _POSIX_C_SOURCE=200809L
    CLAMBHOOK_VERSION="${PROJECT_VERSION}"
  )
  if(CLAMBHOOK_BUILD_TESTS)
    add_executable(clambhook-linux-model-tests
      ui/linux-gtk/tests/test_model.c
    )
    target_link_libraries(clambhook-linux-model-tests PRIVATE
      clambhook-linux-model
    )
    target_compile_options(clambhook-linux-model-tests PRIVATE
      $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
    )
    add_test(NAME linux-gtk-model COMMAND clambhook-linux-model-tests)
    add_test(NAME linux-gtk-version COMMAND clambhook-linux-c --version)
    set_tests_properties(linux-gtk-version PROPERTIES
      PASS_REGULAR_EXPRESSION "^clambhook-linux ${PROJECT_VERSION}"
    )
  endif()
endif()

if(CLAMBHOOK_BUILD_JNI)
  find_package(JNI REQUIRED)
  add_library(clambhook_jni SHARED
    native/src/android/jni.c
    native/src/android/rule_feed_jni.c
  )
  target_include_directories(clambhook_jni PRIVATE ${JNI_INCLUDE_DIRS})
  target_link_libraries(clambhook_jni PRIVATE Clambhook::Core ${JNI_LIBRARIES})
  target_compile_options(clambhook_jni PRIVATE
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
  )
  set_target_properties(clambhook_jni PROPERTIES OUTPUT_NAME clambhook_jni)
endif()

if(CLAMBHOOK_BUILD_TESTS)
  add_executable(clambhook-native-tests
    native/src/api_server.c
    native/tests/test_api_server.c
    native/tests/test_config.c
    native/tests/test_conditioner.c
    native/tests/test_main.c
    native/tests/test_events.c
    native/tests/test_crypto.c
    native/tests/test_developer.c
    native/tests/test_dns.c
    native/tests/test_ip_stack.c
    native/tests/test_json.c
    native/tests/test_license.c
    native/tests/test_listener.c
    native/tests/test_netwatch.c
    native/tests/test_openvpn.c
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
  )
  target_link_libraries(clambhook-native-tests PRIVATE Clambhook::Core PkgConfig::LLHTTP)
  add_test(NAME native-unit COMMAND clambhook-native-tests)
  if(CLAMBHOOK_BUILD_TUI)
    add_test(NAME native-tui-version COMMAND clambhook-tui-c --version)
    set_tests_properties(native-tui-version PROPERTIES
      PASS_REGULAR_EXPRESSION "^clambhook-tui ${PROJECT_VERSION}"
    )
  endif()
endif()

install(TARGETS clambhook_core clambhook_crypto
  EXPORT ClambhookTargets
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
)
install(DIRECTORY native/include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(FILES clib/include/cnet.h DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(FILES LICENSE LICENSE-APACHE LICENSING.md NOTICE TRADEMARKS.md
  THIRD_PARTY_NOTICES.md
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook)
install(FILES third_party/openssl/LICENSE.txt
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook/licenses/openssl)
install(FILES third_party/curl/LICENSE.txt
  DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/doc/clambhook/licenses/curl)
