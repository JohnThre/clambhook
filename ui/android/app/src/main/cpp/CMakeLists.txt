cmake_minimum_required(VERSION 3.22)
project(clambhook_android LANGUAGES C)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)

get_filename_component(CLAMBHOOK_ROOT "${CMAKE_CURRENT_LIST_DIR}/../../../../../.." ABSOLUTE)

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
    "${CLAMBHOOK_ROOT}/native/src/android/jni.c"
    "${CLAMBHOOK_ROOT}/native/src/android/runtime_android.c"
    "${CLAMBHOOK_ROOT}/native/src/config.c"
    "${CLAMBHOOK_ROOT}/native/src/config_payload.c"
    "${CLAMBHOOK_ROOT}/native/src/error.c"
    "${CLAMBHOOK_ROOT}/native/src/ip_stack.c"
    "${CLAMBHOOK_ROOT}/native/src/json.c"
    "${CLAMBHOOK_ROOT}/native/src/rule_feed.c"
    "${CLAMBHOOK_ROOT}/native/src/rule_feed_config.c"
    "${CLAMBHOOK_ROOT}/native/src/rules.c"
    "${CLAMBHOOK_ROOT}/native/src/rules_config.c"
)

target_include_directories(clambhook_jni PRIVATE
    "${CLAMBHOOK_ROOT}/native/include"
    "${CLAMBHOOK_ROOT}/native/src"
    "${CLAMBHOOK_ROOT}/third_party/tomlc99"
)
target_compile_definitions(clambhook_jni PRIVATE _POSIX_C_SOURCE=200809L)
target_compile_options(clambhook_jni PRIVATE
    -Wall -Wextra -Wpedantic -Wconversion -Wshadow
)
target_link_libraries(clambhook_jni PRIVATE log lwipcore tomlc99)
