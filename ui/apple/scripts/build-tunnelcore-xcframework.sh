#!/bin/sh
set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
APPLE_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
REPO_ROOT=$(cd "$APPLE_DIR/../.." && pwd)

MODULE_NAME=ClambhookTunnelCore
OUT_DIR="$APPLE_DIR/Frameworks"
BUILD_ROOT="${DERIVED_FILE_DIR:-$APPLE_DIR/.build}/tunnelcore"
HEADER_DIR="$BUILD_ROOT/headers"
DEVICE_DIR="$BUILD_ROOT/iphoneos-arm64"
SIM_ARM64_DIR="$BUILD_ROOT/iphonesimulator-arm64"
SIM_X86_64_DIR="$BUILD_ROOT/iphonesimulator-x86_64"
SIM_UNIVERSAL_DIR="$BUILD_ROOT/iphonesimulator-universal"
LINK_DIR="$BUILD_ROOT/link"

mkdir -p "$OUT_DIR" "$HEADER_DIR" "$DEVICE_DIR" "$SIM_ARM64_DIR" "$SIM_X86_64_DIR" "$SIM_UNIVERSAL_DIR" "$LINK_DIR"

build_archive() {
    sdk="$1"
    goarch="$2"
    clang_arch="$3"
    min_flag="$4"
    output_dir="$5"

    sdk_path=$(xcrun --sdk "$sdk" --show-sdk-path)
    clang_path=$(xcrun --sdk "$sdk" --find clang)

    (
        cd "$REPO_ROOT"
        CGO_ENABLED=1 \
        GOOS=ios \
        GOARCH="$goarch" \
        DARWIN_SDK="$sdk" \
        CC="$clang_path" \
        CXX="$clang_path++" \
        CGO_CFLAGS="-isysroot $sdk_path $min_flag -fembed-bitcode -arch $clang_arch" \
        CGO_CXXFLAGS="-isysroot $sdk_path $min_flag -fembed-bitcode -arch $clang_arch" \
        CGO_LDFLAGS="-isysroot $sdk_path $min_flag -fembed-bitcode -arch $clang_arch" \
        go build -tags ios -trimpath -buildmode=c-archive -o "$output_dir/lib$MODULE_NAME.a" ./mobile/iosbridge
    )
}

build_archive iphoneos arm64 arm64 "-miphoneos-version-min=17.0" "$DEVICE_DIR"
build_archive iphonesimulator arm64 arm64 "-mios-simulator-version-min=17.0" "$SIM_ARM64_DIR"
build_archive iphonesimulator amd64 x86_64 "-mios-simulator-version-min=17.0" "$SIM_X86_64_DIR"

xcrun lipo -create \
    "$SIM_ARM64_DIR/lib$MODULE_NAME.a" \
    "$SIM_X86_64_DIR/lib$MODULE_NAME.a" \
    -output "$SIM_UNIVERSAL_DIR/lib$MODULE_NAME.a"

cp "$DEVICE_DIR/lib$MODULE_NAME.h" "$HEADER_DIR/$MODULE_NAME.h"
cat > "$HEADER_DIR/module.modulemap" <<EOF
module $MODULE_NAME {
    header "$MODULE_NAME.h"
    export *
}
EOF

rm -rf "$OUT_DIR/$MODULE_NAME.xcframework"
xcodebuild -create-xcframework \
    -library "$DEVICE_DIR/lib$MODULE_NAME.a" \
    -headers "$HEADER_DIR" \
    -library "$SIM_UNIVERSAL_DIR/lib$MODULE_NAME.a" \
    -headers "$HEADER_DIR" \
    -output "$OUT_DIR/$MODULE_NAME.xcframework"

if [ "${PLATFORM_NAME:-}" = "iphonesimulator" ]; then
    cp "$SIM_UNIVERSAL_DIR/lib$MODULE_NAME.a" "$LINK_DIR/lib$MODULE_NAME.a"
else
    cp "$DEVICE_DIR/lib$MODULE_NAME.a" "$LINK_DIR/lib$MODULE_NAME.a"
fi
