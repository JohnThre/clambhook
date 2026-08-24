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

add_library(clambhook_jni SHARED
    "${CLAMBHOOK_ROOT}/native/src/android/jni.c"
    "${CLAMBHOOK_ROOT}/native/src/android/runtime_android.c"
    "${CLAMBHOOK_ROOT}/native/src/config.c"
    "${CLAMBHOOK_ROOT}/native/src/config_payload.c"
    "${CLAMBHOOK_ROOT}/native/src/error.c"
    "${CLAMBHOOK_ROOT}/native/src/json.c"
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
target_link_libraries(clambhook_jni PRIVATE log tomlc99)
