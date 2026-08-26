#include "test.h"

#include <stdlib.h>

#include "clambhook/json.h"
#include "internal.h"

void ch_test_json(void) {
    ch_json_buffer json;
    ch_json_init(&json);
    CH_TEST_ASSERT(ch_json_append(&json, "{\"value\":"));
    CH_TEST_ASSERT(ch_json_append_string(&json, "quote=\" newline=\n tab=\t control=\x01"));
    CH_TEST_ASSERT(ch_json_append_format(&json, ",\"count\":%d}", 3));
    char *value = ch_json_take(&json);
    CH_TEST_ASSERT_STRING(
        "{\"value\":\"quote=\\\" newline=\\n tab=\\t control=\\u0001\",\"count\":3}",
        value
    );
    free(value);

    const char document[] =
        "{\"name\":\"Clambhook \\uD83E\\uDD80\",\"enabled\":true,"
        "\"numbers\":[0,-12.5,6.02e23],\"nothing\":null}";
    ch_error error;
    ch_json_value *root = ch_json_parse(document, sizeof(document) - 1U, &error);
    CH_TEST_ASSERT(root != NULL && ch_json_value_type(root) == CH_JSON_OBJECT);
    CH_TEST_ASSERT_STRING(
        "Clambhook \xf0\x9f\xa6\x80",
        ch_json_string_value(ch_json_object_get(root, "name"))
    );
    CH_TEST_ASSERT(ch_json_bool_value(ch_json_object_get(root, "enabled"), false));
    const ch_json_value *numbers = ch_json_object_get(root, "numbers");
    CH_TEST_ASSERT(ch_json_array_size(numbers) == 3U);
    CH_TEST_ASSERT(ch_json_number_value(ch_json_array_get(numbers, 1U), 0.0) == -12.5);
    ch_json_value *copy = ch_json_value_clone(root);
    CH_TEST_ASSERT(copy != NULL);
    CH_TEST_ASSERT(ch_json_object_set(
        copy, "enabled", ch_json_value_new_bool(false), &error) == CH_OK);
    CH_TEST_ASSERT(!ch_json_bool_value(ch_json_object_get(copy, "enabled"),
                                      true));
    CH_TEST_ASSERT(ch_json_bool_value(ch_json_object_get(root, "enabled"),
                                     false));
    CH_TEST_ASSERT(ch_json_object_set(
        copy, "label", ch_json_value_new_string("native"), &error) == CH_OK);
    CH_TEST_ASSERT_STRING(
        "native", ch_json_string_value(ch_json_object_get(copy, "label")));
    CH_TEST_ASSERT(ch_json_object_remove(copy, "nothing"));
    CH_TEST_ASSERT(ch_json_object_get(copy, "nothing") == NULL);
    CH_TEST_ASSERT(ch_json_array_get_mutable(
        ch_json_object_get_mutable(copy, "numbers"), 2U) != NULL);
    ch_json_value *new_number = ch_json_value_new_number(7.0);
    CH_TEST_ASSERT(new_number != NULL);
    CH_TEST_ASSERT(ch_json_array_append(
        ch_json_object_get_mutable(copy, "numbers"), new_number, &error) ==
        CH_OK);
    CH_TEST_ASSERT(ch_json_array_size(
        ch_json_object_get(copy, "numbers")) == 4U);
    CH_TEST_ASSERT(ch_json_number_value(
        ch_json_array_get(ch_json_object_get(copy, "numbers"), 3U), 0.0) ==
        7.0);
    ch_json_value_destroy(copy);
    ch_json_value_destroy(root);

    const char invalid[] = "{\"bad\": [1,]}";
    CH_TEST_ASSERT(ch_json_parse(invalid, sizeof(invalid) - 1U, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_PARSE);

    const char bad_surrogate[] = "\"\\uD800x\"";
    CH_TEST_ASSERT(ch_json_parse(bad_surrogate, sizeof(bad_surrogate) - 1U, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_PARSE);
}
