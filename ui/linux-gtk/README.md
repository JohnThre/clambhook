# Clambhook GTK 4 client migration

This directory is the additive C/GTK 4 replacement for the existing
Kotlin/Compose Desktop client. Until the full feature-parity gate passes it is
built as `clambhook-linux-c`; packaging continues to ship the existing client.

Build and run the current migration slice with:

```sh
cmake -S . -B build-native -G Ninja -DCLAMBHOOK_BUILD_GTK=ON
cmake --build build-native --target clambhook-linux-c
CLAMBHOOK_API_URL=http://127.0.0.1:9090 build-native/clambhook-linux-c
```

The controller already uses `GtkApplication`, asynchronous libsoup requests,
and the existing `/api/v1/status`, `/connect`, and `/disconnect` contracts. New
dashboard sections will replace the Compose client incrementally while the old
client remains the visual and behavioral parity oracle.
