cmake_minimum_required(VERSION 3.22)
project(clambhook_android LANGUAGES C)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)

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
file(MAKE_DIRECTORY "${CLAMBHOOK_ANDROID_OPENSSL_INSTALL}/include")

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

add_library(tomlc99 STATIC
    "${CLAMBHOOK_ROOT}/third_party/tomlc99/toml.c"
)
target_include_directories(tomlc99 PUBLIC "${CLAMBHOOK_ROOT}/third_party/tomlc99")
target_compile_definitions(tomlc99 PRIVATE _POSIX_C_SOURCE=200809L)

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

add_library(clambhook_jni SHARED
    "${CLAMBHOOK_ROOT}/clib/src/crypto.c"
    "${CLAMBHOOK_ROOT}/native/src/android/jni.c"
    "${CLAMBHOOK_ROOT}/native/src/android/runtime_android.c"
    "${CLAMBHOOK_ROOT}/native/src/config.c"
    "${CLAMBHOOK_ROOT}/native/src/config_mutation.c"
    "${CLAMBHOOK_ROOT}/native/src/config_payload.c"
    "${CLAMBHOOK_ROOT}/native/src/error.c"
    "${CLAMBHOOK_ROOT}/native/src/ip_stack.c"
    "${CLAMBHOOK_ROOT}/native/src/json.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_packet.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_shadowtls.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_shadowsocks.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_tor.c"
    "${CLAMBHOOK_ROOT}/native/src/protocol_vmess.c"
    "${CLAMBHOOK_ROOT}/native/src/rule_feed.c"
    "${CLAMBHOOK_ROOT}/native/src/rule_feed_config.c"
    "${CLAMBHOOK_ROOT}/native/src/rules.c"
    "${CLAMBHOOK_ROOT}/native/src/rules_config.c"
    "${CLAMBHOOK_ROOT}/native/src/runtime_config.c"
    "${CLAMBHOOK_ROOT}/native/src/socks.c"
)

add_dependencies(clambhook_jni clambhook_android_openssl)

target_include_directories(clambhook_jni PRIVATE
    "${CLAMBHOOK_ROOT}/native/include"
    "${CLAMBHOOK_ROOT}/native/src"
    "${CLAMBHOOK_ROOT}/clib/include"
    "${CLAMBHOOK_ROOT}/third_party/tomlc99"
)
target_compile_definitions(clambhook_jni PRIVATE _POSIX_C_SOURCE=200809L)
target_compile_options(clambhook_jni PRIVATE
    -Wall -Wextra -Wpedantic -Wconversion -Wshadow
)
target_link_libraries(clambhook_jni PRIVATE
    log dl lwipcore tomlc99 clambhook_android_ssl clambhook_android_crypto
)
