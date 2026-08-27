#ifndef CLAMBHOOK_CONFIG_H
#define CLAMBHOOK_CONFIG_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Opaque views are borrowed from ch_config and remain valid until it is freed. */
typedef struct ch_config ch_config;
typedef struct ch_config_table ch_config_table;
typedef struct ch_config_array ch_config_array;

typedef enum ch_config_array_kind {
    CH_CONFIG_ARRAY_VALUES = 1,
    CH_CONFIG_ARRAY_TABLES = 2,
    CH_CONFIG_ARRAY_ARRAYS = 3,
    CH_CONFIG_ARRAY_MIXED = 4
} ch_config_array_kind;

#define CH_CONFIG_MAX_BACKUPS 5U

/* Loads TOML, applies contract validation, and records the source path. */
ch_status ch_config_load(const char *path, ch_config **out_config, ch_error *error);

/* Parses an in-memory document. source_path may be NULL. */
ch_status ch_config_parse(const char *toml, const char *source_path,
                          ch_config **out_config, ch_error *error);

void ch_config_free(ch_config *config);
const char *ch_config_source_path(const ch_config *config);
const char *ch_config_document(const ch_config *config);
const ch_config_table *ch_config_root(const ch_config *config);

/* Returns a validated document with the top-level active profile replaced. */
ch_status ch_config_document_set_active(const ch_config *config,
                                        const char *profile_name,
                                        char **out_toml,
                                        ch_error *error);

size_t ch_config_profile_count(const ch_config *config);
const ch_config_table *ch_config_profile_at(const ch_config *config, size_t index);
const ch_config_table *ch_config_active_profile(const ch_config *config);
const ch_config_table *ch_config_profile_named(const ch_config *config,
                                                const char *name);

/* Resolves a config-relative path without requiring the target to exist. */
ch_status ch_config_resolve_path(const ch_config *config, const char *configured_path,
                                 char **out_path, ch_error *error);

/* Validates then atomically replaces path, retaining five newest backups. */
ch_status ch_config_write_atomic_document(const char *path, const char *toml,
                                          char **out_backup_path,
                                          ch_error *error);

/*
 * Parses a complete imported TOML document and returns the profile-review JSON
 * used by native GUIs. The returned string is released with free().
 */
ch_status ch_config_import_review_json(const char *import_toml,
                                       char **out_json,
                                       ch_error *error);

/*
 * Applies the reviewed profile selection in request_json to path as one
 * validated atomic transaction. Unselected imported profiles and imported
 * root-level settings are not copied.
 */
ch_status ch_config_apply_reviewed_import_file(const char *path,
                                               const char *request_json,
                                               ch_error *error);

bool ch_config_table_has(const ch_config_table *table, const char *key);
const ch_config_table *ch_config_table_get_table(const ch_config_table *table,
                                                  const char *key);
const ch_config_array *ch_config_table_get_array(const ch_config_table *table,
                                                  const char *key);

/* String results are heap allocated because tomlc99 decodes on access. */
ch_status ch_config_table_get_string(const ch_config_table *table, const char *key,
                                     char **out_value, ch_error *error);
ch_status ch_config_table_get_bool(const ch_config_table *table, const char *key,
                                   bool *out_value, ch_error *error);
ch_status ch_config_table_get_int(const ch_config_table *table, const char *key,
                                  int64_t *out_value, ch_error *error);
ch_status ch_config_table_get_double(const ch_config_table *table, const char *key,
                                     double *out_value, ch_error *error);

size_t ch_config_array_count(const ch_config_array *array);
ch_config_array_kind ch_config_array_get_kind(const ch_config_array *array);
const ch_config_table *ch_config_array_get_table(const ch_config_array *array,
                                                  size_t index);
const ch_config_array *ch_config_array_get_array(const ch_config_array *array,
                                                  size_t index);
ch_status ch_config_array_get_string(const ch_config_array *array, size_t index,
                                     char **out_value, ch_error *error);
ch_status ch_config_array_get_bool(const ch_config_array *array, size_t index,
                                   bool *out_value, ch_error *error);
ch_status ch_config_array_get_int(const ch_config_array *array, size_t index,
                                  int64_t *out_value, ch_error *error);
ch_status ch_config_array_get_double(const ch_config_array *array, size_t index,
                                     double *out_value, ch_error *error);

/* Encodes a borrowed subtree as canonical compact JSON; free with free(). */
ch_status ch_config_table_json(const ch_config_table *table, char **out_json,
                               ch_error *error);
ch_status ch_config_array_json(const ch_config_array *array, char **out_json,
                               ch_error *error);

/* Parses the duration syntax used by the existing TOML contract. */
ch_status ch_config_parse_duration_ns(const char *text, int64_t *out_nanoseconds,
                                      ch_error *error);

/* Default values frozen from the legacy configuration contract. */
enum {
    CH_CONFIG_DEFAULT_TRAFFIC_HISTORY_LIMIT = 500,
    CH_CONFIG_DEFAULT_DEVELOPER_CAPTURE_LIMIT = 200,
    CH_CONFIG_DEFAULT_DEVELOPER_BODY_LIMIT_BYTES = 64 * 1024,
    CH_CONFIG_DEFAULT_DEVELOPER_HEADER_LIMIT_BYTES = 8 * 1024
};

#ifdef __cplusplus
}
#endif

#endif
