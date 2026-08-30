# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

cmake_minimum_required(VERSION 3.22)
project(clambhook_android LANGUAGES C)

include(TestBigEndian)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

get_filename_component(CLAMBHOOK_ROOT "${CMAKE_CURRENT_LIST_DIR}/../../../../../.." ABSOLUTE)

set(CLAMBHOOK_ANDROID_OPENSSL_ROOT
    "${CLAMBHOOK_ROOT}/ui/android/.native-deps"
    CACHE PATH "Shared Android OpenSSL build cache")
string(REGEX REPLACE "^android-" "" CLAMBHOOK_ANDROID_API "${ANDROID_PLATFORM}")
set(CLAMBHOOK_ANDROID_OPENSSL_INSTALL
    "${CLAMBHOOK_ANDROID_OPENSSL_ROOT}/openssl-3.5.8-api${CLAMBHOOK_ANDROID_API}/${ANDROID_ABI}")
set(CLAMBHOOK_ANDROID_OPENSSL_SSL
    "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}/lib/libssl.a")
set(CLAMBHOOK_ANDROID_OPENSSL_CRYPTO
    "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}/lib/libcrypto.a")
set(CLAMBHOOK_ANDROID_CURL_INSTALL
    "${CLAMBHOOK_ANDROID_OPENSSL_ROOT}/curl-8.18.0-api${CLAMBHOOK_ANDROID_API}/${ANDROID_ABI}")
set(CLAMBHOOK_ANDROID_CURL
    "${CLAMBHOOK_ANDROID_CURL_INSTALL}/lib/libcurl.a")
file(MAKE_DIRECTORY "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}/include")
file(MAKE_DIRECTORY "${CLAMBHOOK_ANDROID_CURL_INSTALL}/include")

add_custom_command(
    OUTPUT
        "${CLAMBHOOK_ANDROID_OPENSSL_SSL}"
        "${CLAMBHOOK_ANDROID_OPENSSL_CRYPTO}"
    COMMAND bash "${CLAMBHOOK_ROOT}/scripts/build-android-openssl.sh"
        "${CLAMBHOOK_ANDROID_OPENSSL_ROOT}"
        "${CMAKE_ANDROID_NDK}"
        "${CLAMBHOOK_ANDROID_API}"
        "${ANDROID_ABI}"
    DEPENDS "${CLAMBHOOK_ROOT}/scripts/build-android-openssl.sh"
    COMMENT "Provisioning OpenSSL 3.5.8 for ${ANDROID_ABI}/API ${CLAMBHOOK_ANDROID_API}"
    VERBATIM
)
add_custom_target(clambhook_android_openssl DEPENDS
    "${CLAMBHOOK_ANDROID_OPENSSL_SSL}"
    "${CLAMBHOOK_ANDROID_OPENSSL_CRYPTO}"
)
add_library(clambhook_android_crypto STATIC IMPORTED GLOBAL)
set_target_properties(clambhook_android_crypto PROPERTIES
    IMPORTED_LOCATION "${CLAMBHOOK_ANDROID_OPENSSL_CRYPTO}"
    INTERFACE_INCLUDE_DIRECTORIES "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}/include"
)
add_library(clambhook_android_ssl STATIC IMPORTED GLOBAL)
set_target_properties(clambhook_android_ssl PROPERTIES
    IMPORTED_LOCATION "${CLAMBHOOK_ANDROID_OPENSSL_SSL}"
    INTERFACE_INCLUDE_DIRECTORIES "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}/include"
    INTERFACE_LINK_LIBRARIES clambhook_android_crypto
)

add_custom_command(
    OUTPUT "${CLAMBHOOK_ANDROID_CURL}"
    COMMAND bash "${CLAMBHOOK_ROOT}/scripts/build-android-curl.sh"
        "${CLAMBHOOK_ANDROID_OPENSSL_ROOT}"
        "${CMAKE_ANDROID_NDK}"
        "${CLAMBHOOK_ANDROID_API}"
        "${ANDROID_ABI}"
        "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}"
    DEPENDS
        "${CLAMBHOOK_ROOT}/scripts/build-android-curl.sh"
        "${CLAMBHOOK_ANDROID_OPENSSL_SSL}"
        "${CLAMBHOOK_ANDROID_OPENSSL_CRYPTO}"
    COMMENT "Provisioning curl 8.18.0 for ${ANDROID_ABI}/API ${CLAMBHOOK_ANDROID_API}"
    VERBATIM
)
add_custom_target(clambhook_android_curl DEPENDS "${CLAMBHOOK_ANDROID_CURL}")
add_dependencies(clambhook_android_curl clambhook_android_openssl)
add_library(clambhook_android_libcurl STATIC IMPORTED GLOBAL)
set_target_properties(clambhook_android_libcurl PROPERTIES
    IMPORTED_LOCATION "${CLAMBHOOK_ANDROID_CURL}"
    INTERFACE_INCLUDE_DIRECTORIES "${CLAMBHOOK_ANDROID_CURL_INSTALL}/include"
    INTERFACE_LINK_LIBRARIES "clambhook_android_ssl;clambhook_android_crypto"
)

add_library(tomlc99 STATIC
    "${CLAMBHOOK_ROOT}/third_party/tomlc99/toml.c"
)
target_include_directories(tomlc99 PUBLIC "${CLAMBHOOK_ROOT}/third_party/tomlc99")
target_compile_definitions(tomlc99 PRIVATE _POSIX_C_SOURCE=200809L)

set(LIBYAML_DIR "${CLAMBHOOK_ROOT}/third_party/libyaml")
add_library(libyaml STATIC
    "${LIBYAML_DIR}/src/api.c"
    "${LIBYAML_DIR}/src/loader.c"
    "${LIBYAML_DIR}/src/parser.c"
    "${LIBYAML_DIR}/src/reader.c"
    "${LIBYAML_DIR}/src/scanner.c"
)
target_include_directories(libyaml PUBLIC "${LIBYAML_DIR}/include")
target_include_directories(libyaml PRIVATE "${LIBYAML_DIR}/src")
target_compile_definitions(libyaml PRIVATE
    YAML_VERSION_STRING="0.2.5"
    YAML_VERSION_MAJOR=0
    YAML_VERSION_MINOR=2
    YAML_VERSION_PATCH=5
)

set(MAXMINDDB_DIR "${CLAMBHOOK_ROOT}/third_party/libmaxminddb")
# Match the desktop core's strictly ISO C17 public-header representation.
set(MMDB_UINT128_USING_MODE 0)
set(MMDB_UINT128_IS_BYTE_ARRAY 1)
test_big_endian(CH_MMDB_BIG_ENDIAN)
configure_file(
    "${MAXMINDDB_DIR}/include/maxminddb_config.h.cmake.in"
    "${CMAKE_CURRENT_BINARY_DIR}/generated/maxminddb_config.h"
)
add_library(maxminddb STATIC
    "${MAXMINDDB_DIR}/src/maxminddb.c"
    "${MAXMINDDB_DIR}/src/data-pool.c"
)
target_include_directories(maxminddb
    PUBLIC "${MAXMINDDB_DIR}/include" "${CMAKE_CURRENT_BINARY_DIR}/generated"
    PRIVATE "${MAXMINDDB_DIR}/src"
)
target_compile_definitions(maxminddb PRIVATE PACKAGE_VERSION="1.13.3")
if(NOT CH_MMDB_BIG_ENDIAN)
    target_compile_definitions(maxminddb PRIVATE MMDB_LITTLE_ENDIAN=1)
endif()

set(LWIP_DIR "${CLAMBHOOK_ROOT}/third_party/lwip")
add_library(lwipcore STATIC
    "${LWIP_DIR}/src/core/init.c"
    "${LWIP_DIR}/src/core/def.c"
    "${LWIP_DIR}/src/core/inet_chksum.c"
    "${LWIP_DIR}/src/core/ip.c"
    "${LWIP_DIR}/src/core/mem.c"
    "${LWIP_DIR}/src/core/memp.c"
    "${LWIP_DIR}/src/core/netif.c"
    "${LWIP_DIR}/src/core/pbuf.c"
    "${LWIP_DIR}/src/core/stats.c"
    "${LWIP_DIR}/src/core/sys.c"
    "${LWIP_DIR}/src/core/tcp.c"
    "${LWIP_DIR}/src/core/tcp_in.c"
    "${LWIP_DIR}/src/core/tcp_out.c"
    "${LWIP_DIR}/src/core/timeouts.c"
    "${LWIP_DIR}/src/core/udp.c"
    "${LWIP_DIR}/src/core/ipv4/icmp.c"
    "${LWIP_DIR}/src/core/ipv4/ip4.c"
    "${LWIP_DIR}/src/core/ipv4/ip4_addr.c"
    "${LWIP_DIR}/src/core/ipv4/ip4_frag.c"
    "${LWIP_DIR}/src/core/ipv6/icmp6.c"
    "${LWIP_DIR}/src/core/ipv6/inet6.c"
    "${LWIP_DIR}/src/core/ipv6/ip6.c"
    "${LWIP_DIR}/src/core/ipv6/ip6_addr.c"
    "${LWIP_DIR}/src/core/ipv6/ip6_frag.c"
    "${LWIP_DIR}/src/core/ipv6/nd6.c"
)
target_include_directories(lwipcore PUBLIC
    "${LWIP_DIR}/src/include"
    "${CLAMBHOOK_ROOT}/native/src"
)

set(WIREGUARD_LWIP_DIR "${CLAMBHOOK_ROOT}/third_party/wireguard_lwip")
add_library(wireguard_protocol STATIC
    "${WIREGUARD_LWIP_DIR}/src/crypto.c"
    "${WIREGUARD_LWIP_DIR}/src/wireguard.c"
    "${WIREGUARD_LWIP_DIR}/src/crypto/refc/blake2s.c"
    "${WIREGUARD_LWIP_DIR}/src/crypto/refc/chacha20.c"
    "${WIREGUARD_LWIP_DIR}/src/crypto/refc/chacha20poly1305.c"
    "${WIREGUARD_LWIP_DIR}/src/crypto/refc/poly1305-donna.c"
    "${WIREGUARD_LWIP_DIR}/src/crypto/refc/x25519.c"
)
target_include_directories(wireguard_protocol PUBLIC
    "${WIREGUARD_LWIP_DIR}/src"
    "${LWIP_DIR}/src/include"
    "${CLAMBHOOK_ROOT}/native/src"
)
target_compile_definitions(wireguard_protocol PRIVATE
    poly1305_init=ch_wg_poly1305_init
    poly1305_update=ch_wg_poly1305_update
    poly1305_finish=ch_wg_poly1305_finish
)

add_library(clambhook_jni SHARED
    "${CLAMBHOOK_ROOT}/clib/src/crypto.c"
    "${CLAMBHOOK_ROOT}/native/src/android/jni.c"
    "${CLAMBHOOK_ROOT}/native/src/android/license_jni.c"
    "${CLAMBHOOK_ROOT}/native/src/android/runtime_android.c"
    "${CLAMBHOOK_ROOT}/native/src/conditioner.c"
    "${CLAMBHOOK_ROOT}/native/src/config.c"
    "${CLAMBHOOK_ROOT}/native/src/config_import.c"
    "${CLAMBHOOK_ROOT}/native/src/config_mutation.c"
    "${CLAMBHOOK_ROOT}/native/src/config_payload.c"
    "${CLAMBHOOK_ROOT}/native/src/developer.c"
    "${CLAMBHOOK_ROOT}/native/src/developer_curl.c"
    "${CLAMBHOOK_ROOT}/native/src/dns.c"
    "${CLAMBHOOK_ROOT}/native/src/dns_quic.c"
    "${CLAMBHOOK_ROOT}/native/src/error.c"
    "${CLAMBHOOK_ROOT}/native/src/events.c"
    "${CLAMBHOOK_ROOT}/native/src/geo.c"
    "${CLAMBHOOK_ROOT}/native/src/http_safety.c"
    "${CLAMBHOOK_ROOT}/native/src/ip_stack.c"
    "${CLAMBHOOK_ROOT}/native/src/json.c"
    "${CLAMBHOOK_ROOT}/native/src/license.c"
    "${CLAMBHOOK_ROOT}/native/src/license_json.c"
    "${CLAMBHOOK_ROOT}/native/src/lwip_context.c"
    "${CLAMBHOOK_ROOT}/native/src/outline.c"
    "${CLAMBHOOK_ROOT}/native/src/policy.c"
    "${CLAMBHOOK_ROOT}/native/src/profile_converter.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_openvpn.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_packet.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_shadowtls.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_shadowsocks.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_tor.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_vmess.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_wireguard.c"
    "${CLAMBHOOK_ROOT}/native/src/prompt.c"
    "${CLAMBHOOK_ROOT}/native/src/rule_feed.c"
    "${CLAMBHOOK_ROOT}/native/src/rule_feed_config.c"
    "${CLAMBHOOK_ROOT}/native/src/rules.c"
    "${CLAMBHOOK_ROOT}/native/src/rules_config.c"
    "${CLAMBHOOK_ROOT}/native/src/runtime_config.c"
    "${CLAMBHOOK_ROOT}/native/src/socks.c"
    "${CLAMBHOOK_ROOT}/native/src/temporary_rules.c"
    "${CLAMBHOOK_ROOT}/native/src/traffic.c"
    "${CLAMBHOOK_ROOT}/native/src/tunnel_stack.c"
    "${CLAMBHOOK_ROOT}/native/src/wireguard_platform.c"
)

add_library(clambhook_gluon_bridge STATIC
    "${CLAMBHOOK_ROOT}/native/src/android/gluon_bridge.c"
)
set_target_properties(clambhook_gluon_bridge PROPERTIES
    OUTPUT_NAME clambhook_gluon_bridge
    ARCHIVE_OUTPUT_DIRECTORY
        "${CMAKE_CURRENT_LIST_DIR}/../../../build/gluon/${CMAKE_BUILD_TYPE}/${ANDROID_ABI}"
)
target_compile_options(clambhook_gluon_bridge PRIVATE
    -Wall -Wextra -Wpedantic -Wconversion -Wshadow
)

add_dependencies(clambhook_jni clambhook_android_openssl clambhook_android_curl)
add_dependencies(clambhook_jni clambhook_gluon_bridge)

target_include_directories(clambhook_jni PRIVATE
    "${CLAMBHOOK_ROOT}/native/include"
    "${CLAMBHOOK_ROOT}/native/src"
    "${CLAMBHOOK_ROOT}/clib/include"
    "${CLAMBHOOK_ROOT}/third_party/tomlc99"
    "${CLAMBHOOK_ROOT}/third_party/libyaml/include"
)
target_compile_definitions(clambhook_jni PRIVATE
    _POSIX_C_SOURCE=200809L
    CURL_STATICLIB
)
target_compile_options(clambhook_jni PRIVATE
    -Wall -Wextra -Wpedantic -Wconversion -Wshadow
)
target_link_libraries(clambhook_jni PRIVATE
    log dl lwipcore maxminddb wireguard_protocol tomlc99 libyaml clambhook_android_libcurl
    clambhook_android_ssl clambhook_android_crypto
)
