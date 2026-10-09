// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.json

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.JsonUnquotedLiteral
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.longOrNull

/**
 * Lenient, typed reads over the frozen runtime JSON contracts. Missing or
 * mistyped members fall back to caller-supplied defaults so views never crash
 * on additive contract changes.
 */
object Json {
    /**
     * Strictly parses one RFC 8259 document. Duplicate keys, leading zeros,
     * unescaped control characters, and trailing data are rejected so that
     * security-relevant documents are never ambiguous.
     */
    fun parse(source: String): JsonNode {
        val parser = StrictParser(source)
        val value = parser.readValue()
        parser.skipWhitespace()
        if (!parser.atEnd()) throw parser.error("unexpected trailing data")
        return JsonNode(value)
    }

    /** Encodes a JSON object whose values are strings, numbers, booleans, collections, or nodes. */
    fun obj(vararg members: Pair<String, Any?>): String = JsonObject(
        members.associate { (key, value) -> key to element(value) },
    ).toString()

    fun element(value: Any?): JsonElement = when (value) {
        null -> JsonNull
        is JsonNode -> value.element ?: JsonNull
        is JsonElement -> value
        is String -> JsonPrimitive(value)
        is Boolean -> JsonPrimitive(value)
        is Number -> JsonPrimitive(value)
        is Map<*, *> -> JsonObject(value.entries.associate { (key, item) -> key.toString() to element(item) })
        is Iterable<*> -> JsonArray(value.map(::element))
        is Array<*> -> JsonArray(value.map(::element))
        else -> JsonPrimitive(value.toString())
    }
}

/** Null-safe view of one JSON value. A missing member has a null [element]. */
class JsonNode internal constructor(val element: JsonElement?) {
    operator fun get(key: String): JsonNode =
        JsonNode((element as? JsonObject)?.get(key))

    fun elements(): List<JsonNode> =
        (element as? JsonArray)?.map(::JsonNode).orEmpty()

    fun fields(): Map<String, JsonNode> =
        (element as? JsonObject)?.mapValues { JsonNode(it.value) }.orEmpty()

    fun text(fallback: String = ""): String {
        val primitive = element as? JsonPrimitive ?: return fallback
        return if (primitive.isString) primitive.content else fallback
    }

    fun bool(fallback: Boolean): Boolean {
        val primitive = element as? JsonPrimitive ?: return fallback
        if (primitive.isString || primitive is JsonNull) return fallback
        return primitive.booleanOrNull ?: fallback
    }

    fun long(fallback: Long): Long {
        val primitive = element as? JsonPrimitive ?: return fallback
        if (primitive.isString || primitive is JsonNull) return fallback
        primitive.longOrNull?.let { return it }
        val number = primitive.doubleOrNull ?: return fallback
        return if (number % 1.0 == 0.0 && number >= Long.MIN_VALUE && number <= Long.MAX_VALUE) {
            number.toLong()
        } else {
            fallback
        }
    }

    fun double(fallback: Double): Double {
        val primitive = element as? JsonPrimitive ?: return fallback
        if (primitive.isString || primitive is JsonNull) return fallback
        return primitive.doubleOrNull ?: fallback
    }

    val exists: Boolean get() = element != null
    val isNull: Boolean get() = element is JsonNull
    val isObject: Boolean get() = element is JsonObject
    val isArray: Boolean get() = element is JsonArray

    /** Compact JSON encoding; a missing member encodes as `null`. */
    override fun toString(): String = (element ?: JsonNull).toString()
}

private class StrictParser(private val source: String) {
    private var position = 0

    fun readValue(): JsonElement {
        skipWhitespace()
        if (atEnd()) throw error("expected a JSON value")
        return when (source[position]) {
            '{' -> readObject()
            '[' -> readArray()
            '"' -> JsonPrimitive(readString())
            't' -> readLiteral("true", JsonPrimitive(true))
            'f' -> readLiteral("false", JsonPrimitive(false))
            'n' -> readLiteral("null", JsonNull)
            else -> readNumber()
        }
    }

    private fun readObject(): JsonObject {
        position++
        skipWhitespace()
        val result = LinkedHashMap<String, JsonElement>()
        if (consume('}')) return JsonObject(result)
        while (true) {
            skipWhitespace()
            if (atEnd() || source[position] != '"') throw error("expected an object key")
            val key = readString()
            skipWhitespace()
            expect(':')
            val value = readValue()
            if (key in result) throw error("duplicate object key $key")
            result[key] = value
            skipWhitespace()
            if (consume('}')) return JsonObject(result)
            expect(',')
        }
    }

    private fun readArray(): JsonArray {
        position++
        skipWhitespace()
        val result = ArrayList<JsonElement>()
        if (consume(']')) return JsonArray(result)
        while (true) {
            result += readValue()
            skipWhitespace()
            if (consume(']')) return JsonArray(result)
            expect(',')
        }
    }

    private fun readString(): String {
        expect('"')
        val result = StringBuilder()
        while (!atEnd()) {
            val current = source[position++]
            when {
                current == '"' -> return result.toString()
                current == '\\' -> {
                    if (atEnd()) throw error("unterminated escape sequence")
                    when (val escaped = source[position++]) {
                        '"', '\\', '/' -> result.append(escaped)
                        'b' -> result.append('\b')
                        'f' -> result.append('\u000C')
                        'n' -> result.append('\n')
                        'r' -> result.append('\r')
                        't' -> result.append('\t')
                        'u' -> result.append(readUnicodeEscape())
                        else -> throw error("invalid escape sequence")
                    }
                }
                current.code < 0x20 -> throw error("unescaped control character in string")
                else -> result.append(current)
            }
        }
        throw error("unterminated string")
    }

    private fun readUnicodeEscape(): Char {
        if (position + 4 > source.length) throw error("incomplete Unicode escape")
        var value = 0
        repeat(4) {
            val digit = source[position++].digitToIntOrNull(16) ?: throw error("invalid Unicode escape")
            value = value * 16 + digit
        }
        return value.toChar()
    }

    @OptIn(ExperimentalSerializationApi::class)
    private fun readNumber(): JsonElement {
        val start = position
        consume('-')
        if (consume('0')) {
            if (!atEnd() && source[position].isAsciiDigit()) throw error("leading zero in number")
        } else {
            readDigits("expected a number")
        }
        if (consume('.')) readDigits("expected digits after decimal point")
        if (consume('e') || consume('E')) {
            if (!consume('+')) consume('-')
            readDigits("expected exponent digits")
        }
        return JsonUnquotedLiteral(source.substring(start, position))
    }

    private fun readDigits(message: String) {
        val start = position
        while (!atEnd() && source[position].isAsciiDigit()) position++
        if (position == start) throw error(message)
    }

    private fun readLiteral(literal: String, value: JsonElement): JsonElement {
        if (!source.startsWith(literal, position)) throw error("invalid JSON literal")
        position += literal.length
        return value
    }

    private fun consume(expected: Char): Boolean {
        if (!atEnd() && source[position] == expected) {
            position++
            return true
        }
        return false
    }

    private fun expect(expected: Char) {
        if (!consume(expected)) throw error("expected '$expected'")
    }

    fun skipWhitespace() {
        while (!atEnd() && source[position].let { it == ' ' || it == '\n' || it == '\r' || it == '\t' }) position++
    }

    fun atEnd() = position >= source.length

    fun error(message: String) = IllegalArgumentException("$message at character $position")

    private fun Char.isAsciiDigit() = this in '0'..'9'
}
