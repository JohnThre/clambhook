// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.json;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/** Small dependency-free JSON tree used on both the JVM and Gluon native image. */
public final class Json {
    private Json() {
    }

    public static Node parse(String source) {
        Parser parser = new Parser(Objects.requireNonNullElse(source, ""));
        Node value = new Node(parser.readValue());
        parser.skipWhitespace();
        if (!parser.atEnd()) {
            throw parser.error("unexpected trailing data");
        }
        return value;
    }

    public static String object(Map<String, ?> values) {
        StringBuilder output = new StringBuilder();
        writeValue(output, values);
        return output.toString();
    }

    public static String quote(String value) {
        StringBuilder output = new StringBuilder();
        writeString(output, Objects.requireNonNullElse(value, ""));
        return output.toString();
    }

    @SuppressWarnings("unchecked")
    private static void writeValue(StringBuilder output, Object value) {
        if (value instanceof Node node) {
            writeValue(output, node.value);
        } else if (value == null) {
            output.append("null");
        } else if (value instanceof String text) {
            writeString(output, text);
        } else if (value instanceof Boolean || value instanceof Number) {
            output.append(value);
        } else if (value instanceof Map<?, ?> map) {
            output.append('{');
            boolean first = true;
            for (Map.Entry<?, ?> entry : map.entrySet()) {
                if (!(entry.getKey() instanceof String key)) {
                    throw new IllegalArgumentException("JSON object keys must be strings");
                }
                if (!first) {
                    output.append(',');
                }
                first = false;
                writeString(output, key);
                output.append(':');
                writeValue(output, entry.getValue());
            }
            output.append('}');
        } else if (value instanceof Iterable<?> values) {
            output.append('[');
            boolean first = true;
            for (Object item : values) {
                if (!first) {
                    output.append(',');
                }
                first = false;
                writeValue(output, item);
            }
            output.append(']');
        } else {
            throw new IllegalArgumentException("unsupported JSON value " + value.getClass().getName());
        }
    }

    private static void writeString(StringBuilder output, String value) {
        output.append('"');
        for (int index = 0; index < value.length(); index++) {
            char current = value.charAt(index);
            switch (current) {
                case '"' -> output.append("\\\"");
                case '\\' -> output.append("\\\\");
                case '\b' -> output.append("\\b");
                case '\f' -> output.append("\\f");
                case '\n' -> output.append("\\n");
                case '\r' -> output.append("\\r");
                case '\t' -> output.append("\\t");
                default -> {
                    if (current < 0x20) {
                        output.append(String.format("\\u%04x", (int) current));
                    } else {
                        output.append(current);
                    }
                }
            }
        }
        output.append('"');
    }

    public static final class Node {
        private static final Node MISSING = new Node(null, true);

        private final Object value;
        private final boolean missing;

        private Node(Object value) {
            this(value, false);
        }

        private Node(Object value, boolean missing) {
            this.value = value;
            this.missing = missing;
        }

        public Node get(String key) {
            if (value instanceof Map<?, ?> map) {
                Object child = map.get(key);
                return map.containsKey(key) ? new Node(child) : MISSING;
            }
            return MISSING;
        }

        public List<Node> elements() {
            if (!(value instanceof List<?> list)) {
                return List.of();
            }
            List<Node> nodes = new ArrayList<>(list.size());
            for (Object child : list) {
                nodes.add(new Node(child));
            }
            return Collections.unmodifiableList(nodes);
        }

        public Map<String, Node> fields() {
            if (!(value instanceof Map<?, ?> map)) {
                return Map.of();
            }
            Map<String, Node> fields = new LinkedHashMap<>();
            for (Map.Entry<?, ?> entry : map.entrySet()) {
                fields.put((String) entry.getKey(), new Node(entry.getValue()));
            }
            return Collections.unmodifiableMap(fields);
        }

        public String text(String fallback) {
            return value instanceof String text ? text : fallback;
        }

        public String text() {
            return text("");
        }

        public boolean bool(boolean fallback) {
            return value instanceof Boolean result ? result : fallback;
        }

        public long longValue(long fallback) {
            if (value instanceof BigDecimal number) {
                try {
                    return number.longValueExact();
                } catch (ArithmeticException ignored) {
                    return fallback;
                }
            }
            return fallback;
        }

        public double doubleValue(double fallback) {
            return value instanceof BigDecimal number ? number.doubleValue() : fallback;
        }

        public boolean exists() {
            return !missing;
        }

        public boolean isNull() {
            return !missing && value == null;
        }

        public boolean isObject() {
            return value instanceof Map<?, ?>;
        }

        public boolean isArray() {
            return value instanceof List<?>;
        }

        @Override
        public String toString() {
            StringBuilder output = new StringBuilder();
            writeValue(output, value);
            return output.toString();
        }
    }

    private static final class Parser {
        private final String source;
        private int position;

        private Parser(String source) {
            this.source = source;
        }

        private Object readValue() {
            skipWhitespace();
            if (atEnd()) {
                throw error("expected a JSON value");
            }
            return switch (source.charAt(position)) {
                case '{' -> readObject();
                case '[' -> readArray();
                case '"' -> readString();
                case 't' -> readLiteral("true", Boolean.TRUE);
                case 'f' -> readLiteral("false", Boolean.FALSE);
                case 'n' -> readLiteral("null", null);
                default -> readNumber();
            };
        }

        private Map<String, Object> readObject() {
            position++;
            skipWhitespace();
            Map<String, Object> result = new LinkedHashMap<>();
            if (consume('}')) {
                return result;
            }
            for (;;) {
                skipWhitespace();
                if (atEnd() || source.charAt(position) != '"') {
                    throw error("expected an object key");
                }
                String key = readString();
                skipWhitespace();
                expect(':');
                Object value = readValue();
                if (result.containsKey(key)) {
                    throw error("duplicate object key " + key);
                }
                result.put(key, value);
                skipWhitespace();
                if (consume('}')) {
                    return result;
                }
                expect(',');
            }
        }

        private List<Object> readArray() {
            position++;
            skipWhitespace();
            List<Object> result = new ArrayList<>();
            if (consume(']')) {
                return result;
            }
            for (;;) {
                result.add(readValue());
                skipWhitespace();
                if (consume(']')) {
                    return result;
                }
                expect(',');
            }
        }

        private String readString() {
            expect('"');
            StringBuilder result = new StringBuilder();
            while (!atEnd()) {
                char current = source.charAt(position++);
                if (current == '"') {
                    return result.toString();
                }
                if (current == '\\') {
                    if (atEnd()) {
                        throw error("unterminated escape sequence");
                    }
                    char escaped = source.charAt(position++);
                    switch (escaped) {
                        case '"', '\\', '/' -> result.append(escaped);
                        case 'b' -> result.append('\b');
                        case 'f' -> result.append('\f');
                        case 'n' -> result.append('\n');
                        case 'r' -> result.append('\r');
                        case 't' -> result.append('\t');
                        case 'u' -> result.append(readUnicodeEscape());
                        default -> throw error("invalid escape sequence");
                    }
                } else if (current < 0x20) {
                    throw error("unescaped control character in string");
                } else {
                    result.append(current);
                }
            }
            throw error("unterminated string");
        }

        private char readUnicodeEscape() {
            if (position + 4 > source.length()) {
                throw error("incomplete Unicode escape");
            }
            int value = 0;
            for (int index = 0; index < 4; index++) {
                int digit = Character.digit(source.charAt(position++), 16);
                if (digit < 0) {
                    throw error("invalid Unicode escape");
                }
                value = value * 16 + digit;
            }
            return (char) value;
        }

        private Object readNumber() {
            int start = position;
            consume('-');
            if (consume('0')) {
                if (!atEnd() && Character.isDigit(source.charAt(position))) {
                    throw error("leading zero in number");
                }
            } else {
                readDigits("expected a number");
            }
            if (consume('.')) {
                readDigits("expected digits after decimal point");
            }
            if (consume('e') || consume('E')) {
                consume('+');
                consume('-');
                readDigits("expected exponent digits");
            }
            try {
                return new BigDecimal(source.substring(start, position));
            } catch (NumberFormatException error) {
                throw error("invalid number");
            }
        }

        private void readDigits(String message) {
            int start = position;
            while (!atEnd() && Character.isDigit(source.charAt(position))) {
                position++;
            }
            if (position == start) {
                throw error(message);
            }
        }

        private Object readLiteral(String literal, Object value) {
            if (!source.startsWith(literal, position)) {
                throw error("invalid JSON literal");
            }
            position += literal.length();
            return value;
        }

        private boolean consume(char expected) {
            if (!atEnd() && source.charAt(position) == expected) {
                position++;
                return true;
            }
            return false;
        }

        private void expect(char expected) {
            if (!consume(expected)) {
                throw error("expected '" + expected + "'");
            }
        }

        private void skipWhitespace() {
            while (!atEnd()) {
                char current = source.charAt(position);
                if (current == ' ' || current == '\n' || current == '\r' || current == '\t') {
                    position++;
                } else {
                    break;
                }
            }
        }

        private boolean atEnd() {
            return position >= source.length();
        }

        private IllegalArgumentException error(String message) {
            return new IllegalArgumentException(message + " at character " + position);
        }
    }
}
