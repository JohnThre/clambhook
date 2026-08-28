// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.json;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class JsonTest {
    @Test
    void parsesNestedValuesWithoutReflection() {
        Json.Node root = Json.parse("""
                {"name":"ClambHook","running":true,"count":3,
                 "items":["one",null,{"value":2.5}]}
                """);

        assertEquals("ClambHook", root.get("name").text());
        assertTrue(root.get("running").bool(false));
        assertEquals(3, root.get("count").longValue(0));
        assertEquals("one", root.get("items").elements().get(0).text());
        assertTrue(root.get("items").elements().get(1).isNull());
        assertEquals(2.5, root.get("items").elements().get(2)
                .get("value").doubleValue(0), 0.0001);
        assertFalse(root.get("missing").exists());
    }

    @Test
    void serializesRequestsWithEscaping() {
        String json = Json.object(Map.of(
                "name", "line\n\"two\"",
                "enabled", true,
                "values", List.of(1, 2, 3)));
        Json.Node parsed = Json.parse(json);

        assertEquals("line\n\"two\"", parsed.get("name").text());
        assertTrue(parsed.get("enabled").bool(false));
        assertEquals(3, parsed.get("values").elements().size());
    }

    @Test
    void rejectsAmbiguousOrMalformedInput() {
        assertThrows(IllegalArgumentException.class, () -> Json.parse("{\"a\":1,\"a\":2}"));
        assertThrows(IllegalArgumentException.class, () -> Json.parse("[01]"));
        assertThrows(IllegalArgumentException.class, () -> Json.parse("\"unterminated"));
        assertThrows(IllegalArgumentException.class, () -> Json.parse("true false"));
    }
}
