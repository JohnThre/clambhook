cmake_minimum_required(VERSION 3.22)

project(clambhook VERSION 1.0.2 LANGUAGES C)

include(GNUInstallDirs)
include(CTest)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

option(CLAMBHOOK_BUILD_DAEMON "Build the native daemon" ON)
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
pkg_check_modules(OPENSSL REQUIRED IMPORTED_TARGET openssl)
pkg_check_modules(CURL REQUIRED IMPORTED_TARGET libcurl)

# Pinned, unmodified C99 TOML parser. Keep third-party warnings separate from
# the project's strict warning policy; provenance lives beside the source.
add_library(tomlc99 STATIC third_party/tomlc99/toml.c)
target_include_directories(tomlc99 PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/third_party/tomlc99)
target_compile_definitions(tomlc99 PRIVATE _POSIX_C_SOURCE=200809L)

if(CLAMBHOOK_ENABLE_SANITIZERS AND CMAKE_C_COMPILER_ID MATCHES "AppleClang|Clang|GNU")
  add_compile_options(-fno-omit-frame-pointer -fsanitize=address,undefined)
  add_link_options(-fno-omit-frame-pointer -fsanitize=address,undefined)
endif()

add_library(clambhook_core STATIC
  native/src/config.c
  native/src/config_payload.c
  native/src/error.c
  native/src/events.c
  native/src/json.c
  native/src/license.c
  native/src/license_http.c
  native/src/license_json.c
  native/src/listener.c
  native/src/rules.c
  native/src/rules_config.c
  native/src/runtime.c
  native/src/runtime_listener.c
  native/src/socks.c
  native/src/watcher.c
)
add_library(Clambhook::Core ALIAS clambhook_core)

add_library(clambhook_crypto STATIC clib/src/crypto.c)
add_library(Clambhook::Crypto ALIAS clambhook_crypto)
target_include_directories(clambhook_crypto PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/clib/include>
  $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)
target_link_libraries(clambhook_crypto PUBLIC PkgConfig::LIBSODIUM PkgConfig::OPENSSL)
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
target_link_libraries(clambhook_core PUBLIC PkgConfig::LIBUV PkgConfig::CURL Clambhook::Crypto tomlc99 Threads::Threads)
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
  add_executable(clambhook-linux-c ui/linux-gtk/src/main.c)
  target_link_libraries(clambhook-linux-c PRIVATE
    PkgConfig::GTK4 PkgConfig::LIBSOUP PkgConfig::JSON_GLIB
  )
  target_compile_options(clambhook-linux-c PRIVATE
    $<$<C_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic;-Wconversion;-Wshadow>
  )
  target_compile_definitions(clambhook-linux-c PRIVATE _POSIX_C_SOURCE=200809L)
endif()

if(CLAMBHOOK_BUILD_JNI)
  find_package(JNI REQUIRED)
  add_library(clambhook_jni SHARED native/src/android/jni.c)
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
    native/tests/test_main.c
    native/tests/test_events.c
    native/tests/test_crypto.c
    native/tests/test_json.c
    native/tests/test_license.c
    native/tests/test_listener.c
    native/tests/test_rules.c
    native/tests/test_runtime.c
    native/tests/test_runtime_listener.c
    native/tests/test_socks.c
    native/tests/test_watcher.c
  )
  target_include_directories(clambhook-native-tests PRIVATE native/src native/tests)
  target_compile_definitions(clambhook-native-tests PRIVATE
    CLAMBHOOK_SOURCE_DIR="${CMAKE_CURRENT_SOURCE_DIR}"
  )
  target_link_libraries(clambhook-native-tests PRIVATE Clambhook::Core PkgConfig::LLHTTP)
  add_test(NAME native-unit COMMAND clambhook-native-tests)
endif()

install(TARGETS clambhook_core clambhook_crypto
  EXPORT ClambhookTargets
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
)
install(DIRECTORY native/include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(FILES clib/include/cnet.h DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
