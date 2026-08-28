// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

/** Native parser/cache boundary for rule-set bodies fetched by Android. */
internal object NativeClambhookRuleFeedBridge {
    @Synchronized
    fun metadata(configPath: String, profile: String, name: String, url: String): String =
        nativeMetadata(configPath, profile, name, url)

    @Synchronized
    fun storeResponse(
        configPath: String,
        profile: String,
        name: String,
        url: String,
        format: String,
        body: ByteArray,
        etag: String,
        lastModified: String,
        fetchedTsNs: Long,
    ) = nativeStoreResponse(
        configPath,
        profile,
        name,
        url,
        format,
        body,
        etag,
        lastModified,
        fetchedTsNs,
    )

    @Synchronized
    fun touch(
        configPath: String,
        profile: String,
        name: String,
        url: String,
        fetchedTsNs: Long,
    ) = nativeTouch(configPath, profile, name, url, fetchedTsNs)

    private external fun nativeMetadata(
        configPath: String,
        profile: String,
        name: String,
        url: String,
    ): String
    private external fun nativeStoreResponse(
        configPath: String,
        profile: String,
        name: String,
        url: String,
        format: String,
        body: ByteArray,
        etag: String,
        lastModified: String,
        fetchedTsNs: Long,
    )
    private external fun nativeTouch(
        configPath: String,
        profile: String,
        name: String,
        url: String,
        fetchedTsNs: Long,
    )

    init {
        System.loadLibrary("clambhook_jni")
    }
}
