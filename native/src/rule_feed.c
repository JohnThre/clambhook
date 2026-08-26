#include "clambhook/rule_feed.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#ifndef __ANDROID__
#include <netdb.h>
#include <pthread.h>
#include <sys/socket.h>

#include <curl/curl.h>
#endif

#include "clambhook/json.h"
#include "internal.h"

typedef struct feed_strings {
    char **items;
    size_t count;
    size_t capacity;
} feed_strings;

typedef struct feed_sha256 {
    uint32_t state[8];
    uint64_t bits;
    unsigned char block[64];
    size_t used;
} feed_sha256;

static uint32_t feed_rotr32(uint32_t value, unsigned count) {
    return (value >> count) | (value << (32U - count));
}

static uint32_t feed_load_be32(const unsigned char *bytes) {
    return ((uint32_t)bytes[0] << 24U) | ((uint32_t)bytes[1] << 16U) |
        ((uint32_t)bytes[2] << 8U) | (uint32_t)bytes[3];
}

static void feed_store_be32(unsigned char *bytes, uint32_t value) {
    bytes[0] = (unsigned char)(value >> 24U);
    bytes[1] = (unsigned char)(value >> 16U);
    bytes[2] = (unsigned char)(value >> 8U);
    bytes[3] = (unsigned char)value;
}

static void feed_sha256_transform(feed_sha256 *hash,
                                  const unsigned char block[64]) {
    static const uint32_t constants[64] = {
        0x428a2f98U,0x71374491U,0xb5c0fbcfU,0xe9b5dba5U,
        0x3956c25bU,0x59f111f1U,0x923f82a4U,0xab1c5ed5U,
        0xd807aa98U,0x12835b01U,0x243185beU,0x550c7dc3U,
        0x72be5d74U,0x80deb1feU,0x9bdc06a7U,0xc19bf174U,
        0xe49b69c1U,0xefbe4786U,0x0fc19dc6U,0x240ca1ccU,
        0x2de92c6fU,0x4a7484aaU,0x5cb0a9dcU,0x76f988daU,
        0x983e5152U,0xa831c66dU,0xb00327c8U,0xbf597fc7U,
        0xc6e00bf3U,0xd5a79147U,0x06ca6351U,0x14292967U,
        0x27b70a85U,0x2e1b2138U,0x4d2c6dfcU,0x53380d13U,
        0x650a7354U,0x766a0abbU,0x81c2c92eU,0x92722c85U,
        0xa2bfe8a1U,0xa81a664bU,0xc24b8b70U,0xc76c51a3U,
        0xd192e819U,0xd6990624U,0xf40e3585U,0x106aa070U,
        0x19a4c116U,0x1e376c08U,0x2748774cU,0x34b0bcb5U,
        0x391c0cb3U,0x4ed8aa4aU,0x5b9cca4fU,0x682e6ff3U,
        0x748f82eeU,0x78a5636fU,0x84c87814U,0x8cc70208U,
        0x90befffaU,0xa4506cebU,0xbef9a3f7U,0xc67178f2U
    };
    uint32_t words[64];
    for (size_t index = 0U; index < 16U; ++index) {
        words[index] = feed_load_be32(block + index * 4U);
    }
    for (size_t index = 16U; index < 64U; ++index) {
        uint32_t first = words[index - 15U];
        uint32_t second = words[index - 2U];
        uint32_t sigma0 = feed_rotr32(first, 7U) ^
            feed_rotr32(first, 18U) ^ (first >> 3U);
        uint32_t sigma1 = feed_rotr32(second, 17U) ^
            feed_rotr32(second, 19U) ^ (second >> 10U);
        words[index] = words[index - 16U] + sigma0 +
            words[index - 7U] + sigma1;
    }
    uint32_t a = hash->state[0];
    uint32_t b = hash->state[1];
    uint32_t c = hash->state[2];
    uint32_t d = hash->state[3];
    uint32_t e = hash->state[4];
    uint32_t f = hash->state[5];
    uint32_t g = hash->state[6];
    uint32_t h = hash->state[7];
    for (size_t index = 0U; index < 64U; ++index) {
        uint32_t big1 = feed_rotr32(e, 6U) ^ feed_rotr32(e, 11U) ^
            feed_rotr32(e, 25U);
        uint32_t choose = (e & f) ^ ((~e) & g);
        uint32_t first = h + big1 + choose + constants[index] + words[index];
        uint32_t big0 = feed_rotr32(a, 2U) ^ feed_rotr32(a, 13U) ^
            feed_rotr32(a, 22U);
        uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
        uint32_t second = big0 + majority;
        h = g; g = f; f = e; e = d + first;
        d = c; c = b; b = a; a = first + second;
    }
    hash->state[0] += a; hash->state[1] += b;
    hash->state[2] += c; hash->state[3] += d;
    hash->state[4] += e; hash->state[5] += f;
    hash->state[6] += g; hash->state[7] += h;
}

static void feed_sha256_init(feed_sha256 *hash) {
    *hash = (feed_sha256){
        .state = {0x6a09e667U,0xbb67ae85U,0x3c6ef372U,0xa54ff53aU,
                  0x510e527fU,0x9b05688cU,0x1f83d9abU,0x5be0cd19U}
    };
}

static void feed_sha256_update(feed_sha256 *hash, const void *data,
                               size_t length) {
    const unsigned char *bytes = data;
    hash->bits += (uint64_t)length * 8U;
    while (length > 0U) {
        size_t available = sizeof(hash->block) - hash->used;
        size_t take = length < available ? length : available;
        memcpy(hash->block + hash->used, bytes, take);
        hash->used += take;
        bytes += take;
        length -= take;
        if (hash->used == sizeof(hash->block)) {
            feed_sha256_transform(hash, hash->block);
            hash->used = 0U;
        }
    }
}

static void feed_sha256_final(feed_sha256 *hash, unsigned char output[32]) {
    uint64_t bits = hash->bits;
    hash->block[hash->used++] = 0x80U;
    if (hash->used > 56U) {
        memset(hash->block + hash->used, 0, 64U - hash->used);
        feed_sha256_transform(hash, hash->block);
        hash->used = 0U;
    }
    memset(hash->block + hash->used, 0, 56U - hash->used);
    for (size_t index = 0U; index < 8U; ++index) {
        hash->block[63U - index] = (unsigned char)(bits >> (index * 8U));
    }
    feed_sha256_transform(hash, hash->block);
    for (size_t index = 0U; index < 8U; ++index) {
        feed_store_be32(output + index * 4U, hash->state[index]);
    }
    memset(hash, 0, sizeof(*hash));
}

static void feed_strings_clear(feed_strings *strings) {
    if (strings == NULL) return;
    for (size_t index = 0U; index < strings->count; ++index) {
        free(strings->items[index]);
    }
    free(strings->items);
    memset(strings, 0, sizeof(*strings));
}

static int feed_string_compare(const void *left, const void *right) {
    const char *const *a = left;
    const char *const *b = right;
    return strcmp(*a, *b);
}

static ch_status feed_strings_add_unique(feed_strings *strings,
                                         const char *value,
                                         ch_error *error) {
    for (size_t index = 0U; index < strings->count; ++index) {
        if (strcmp(strings->items[index], value) == 0) return CH_OK;
    }
    if (strings->count >= CH_RULE_FEED_MAX_ENTRIES) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed has more than %u entries",
                     CH_RULE_FEED_MAX_ENTRIES);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (strings->count == strings->capacity) {
        size_t next = strings->capacity == 0U ? 16U : strings->capacity * 2U;
        if (next < strings->capacity ||
            next > SIZE_MAX / sizeof(*strings->items)) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "rule feed entry list is too large");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        char **grown = realloc(strings->items, next * sizeof(*grown));
        if (grown == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "grow rule feed entry list");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        strings->items = grown;
        strings->capacity = next;
    }
    strings->items[strings->count] = ch_strdup(value);
    if (strings->items[strings->count] == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy rule feed entry");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ++strings->count;
    return CH_OK;
}

static char *feed_trim(char *text) {
    while (*text != '\0' && isspace((unsigned char)*text)) ++text;
    char *end = text + strlen(text);
    while (end > text && isspace((unsigned char)end[-1])) --end;
    *end = '\0';
    return text;
}

static int feed_is_ip_address(const char *text) {
    struct in_addr ipv4;
    struct in6_addr ipv6;
    return inet_pton(AF_INET, text, &ipv4) == 1 ||
        inet_pton(AF_INET6, text, &ipv6) == 1;
}

static char *feed_normalize_domain(const char *raw) {
    if (raw == NULL) return NULL;
    char *copy = ch_strdup(raw);
    if (copy == NULL) return NULL;
    char *value = feed_trim(copy);
    size_t length = strlen(value);
    if (length >= 2U && value[0] == '[' && value[length - 1U] == ']') {
        value[length - 1U] = '\0';
        ++value;
    }
    if (strncmp(value, "*.", 2U) == 0) value += 2U;
    length = strlen(value);
    if (length > 0U && value[length - 1U] == '.') value[--length] = '\0';
    for (size_t index = 0U; index < length; ++index) {
        value[index] = (char)tolower((unsigned char)value[index]);
    }
    int valid = length > 0U && value[0] != '.' &&
        strstr(value, "..") == NULL && !feed_is_ip_address(value);
    size_t labels = 0U;
    for (char *cursor = value; valid && *cursor != '\0';) {
        char *dot = strchr(cursor, '.');
        char *end = dot == NULL ? cursor + strlen(cursor) : dot;
        size_t label_length = (size_t)(end - cursor);
        if (label_length == 0U || cursor[0] == '-' || end[-1] == '-') {
            valid = 0;
            break;
        }
        for (char *character = cursor; character < end; ++character) {
            unsigned char byte = (unsigned char)*character;
            if (!((byte >= 'a' && byte <= 'z') ||
                  (byte >= '0' && byte <= '9') || byte == '-' ||
                  byte == '_')) {
                valid = 0;
                break;
            }
        }
        ++labels;
        cursor = dot == NULL ? end : dot + 1U;
    }
    if (labels < 2U) valid = 0;
    char *result = valid ? ch_strdup(value) : NULL;
    free(copy);
    return result;
}

static char *feed_normalize_cidr(const char *raw) {
    if (raw == NULL) return NULL;
    char *copy = ch_strdup(raw);
    if (copy == NULL) return NULL;
    char *value = feed_trim(copy);
    char *slash = strrchr(value, '/');
    int prefix = -1;
    if (slash != NULL) {
        *slash = '\0';
        char *end = NULL;
        errno = 0;
        long parsed = strtol(slash + 1U, &end, 10);
        if (errno != 0 || end == slash + 1U || *end != '\0' ||
            parsed < 0L || parsed > 128L) {
            free(copy);
            return NULL;
        }
        prefix = (int)parsed;
    }
    unsigned char address[16];
    int family = AF_INET;
    size_t bytes = 4U;
    int bits = 32;
    if (inet_pton(AF_INET, value, address) != 1) {
        family = AF_INET6;
        bytes = 16U;
        bits = 128;
        if (inet_pton(AF_INET6, value, address) != 1) {
            free(copy);
            return NULL;
        }
    }
    if (prefix < 0) prefix = bits;
    if (prefix > bits) {
        free(copy);
        return NULL;
    }
    unsigned remaining = (unsigned)prefix;
    for (size_t index = 0U; index < bytes; ++index) {
        unsigned take = remaining >= 8U ? 8U : remaining;
        unsigned mask = take == 0U ? 0U : 0xffU << (8U - take);
        address[index] = (unsigned char)((unsigned)address[index] & mask);
        remaining -= take;
    }
    char encoded[INET6_ADDRSTRLEN];
    if (inet_ntop(family, address, encoded, sizeof(encoded)) == NULL) {
        free(copy);
        return NULL;
    }
    int required = snprintf(NULL, 0, "%s/%d", encoded, prefix);
    char *result = required < 0 ? NULL : malloc((size_t)required + 1U);
    if (result != NULL) {
        (void)snprintf(result, (size_t)required + 1U, "%s/%d", encoded,
                       prefix);
    }
    free(copy);
    return result;
}

static char *feed_url_hostname(const char *url) {
    const char *start = strstr(url, "://");
    if (start == NULL) return NULL;
    start += 3U;
    const char *end = start + strcspn(start, "/?#");
    const char *at = memchr(start, '@', (size_t)(end - start));
    if (at != NULL) start = at + 1U;
    if (start >= end) return NULL;
    if (*start == '[') {
        const char *close = memchr(start + 1U, ']',
                                   (size_t)(end - (start + 1U)));
        if (close == NULL) return NULL;
        ++start;
        end = close;
    } else {
        const char *colon = memchr(start, ':', (size_t)(end - start));
        if (colon != NULL) end = colon;
    }
    size_t length = (size_t)(end - start);
    char *host = malloc(length + 1U);
    if (host == NULL) return NULL;
    memcpy(host, start, length);
    host[length] = '\0';
    return host;
}

static ch_status feed_add_token(feed_strings *domains, feed_strings *cidrs,
                                const char *raw, ch_error *error) {
    char *copy = ch_strdup(raw);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule feed token");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *token = feed_trim(copy);
    size_t length = strlen(token);
    while (length > 0U && (token[0] == '\'' || token[0] == '"')) {
        ++token;
        --length;
    }
    while (length > 0U &&
           (token[length - 1U] == '\'' || token[length - 1U] == '"')) {
        token[--length] = '\0';
    }
    if (strncmp(token, "||", 2U) == 0) token += 2U;
    else if (token[0] == '|') ++token;
    if (strncmp(token, "*.", 2U) == 0) token += 2U;
    length = strlen(token);
    if (length > 0U && token[length - 1U] == '^') token[--length] = '\0';
    if (length > 0U && token[length - 1U] == '.') token[--length] = '\0';
    ch_status status = CH_OK;
    char *normalized = feed_normalize_cidr(token);
    if (normalized != NULL) {
        status = feed_strings_add_unique(cidrs, normalized, error);
        free(normalized);
        free(copy);
        return status;
    }
    if (strncasecmp(token, "http://", 7U) == 0 ||
        strncasecmp(token, "https://", 8U) == 0) {
        char *host = feed_url_hostname(token);
        normalized = feed_normalize_domain(host);
        free(host);
    } else {
        normalized = feed_normalize_domain(token);
    }
    if (normalized != NULL) {
        status = feed_strings_add_unique(domains, normalized, error);
        free(normalized);
    }
    free(copy);
    return status == CH_OK && normalized == NULL ? CH_ERROR_NOT_FOUND : status;
}

static int feed_detect_hosts(const char *line) {
    char *copy = ch_strdup(line);
    if (copy == NULL) return 0;
    char *first = strtok(copy, " \t\r");
    char *second = strtok(NULL, " \t\r");
    int result = first != NULL && second != NULL && feed_is_ip_address(first);
    free(copy);
    return result;
}

static const char *feed_detect_format(const char *body) {
    char *copy = ch_strdup(body);
    if (copy == NULL) return "plain";
    const char *result = "plain";
    char *save = NULL;
    for (char *line = strtok_r(copy, "\n", &save); line != NULL;
         line = strtok_r(NULL, "\n", &save)) {
        line = feed_trim(line);
        if (*line == '\0' || *line == '!' || *line == '#') continue;
        if (strncmp(line, "||", 2U) == 0 || strstr(line, "##") != NULL ||
            strncmp(line, "@@", 2U) == 0) {
            result = "adblock";
            break;
        }
        if (feed_detect_hosts(line)) {
            result = "hosts";
            break;
        }
    }
    free(copy);
    return result;
}

static ch_status feed_parse_plain(char *line, int hosts,
                                  feed_strings *domains,
                                  feed_strings *cidrs, int *usable,
                                  ch_error *error) {
    *usable = 1;
    if (*line == '#' || *line == '!') return CH_OK;
    char *comment = strchr(line, '#');
    if (comment != NULL) *comment = '\0';
    line = feed_trim(line);
    if (*line == '\0') return CH_OK;
    char *save = NULL;
    size_t index = 0U;
    size_t accepted = 0U;
    for (char *token = strtok_r(line, " \t\r", &save); token != NULL;
         token = strtok_r(NULL, " \t\r", &save), ++index) {
        char *remaining = save;
        while (remaining != NULL && *remaining != '\0' &&
               isspace((unsigned char)*remaining)) ++remaining;
        if (hosts && index == 0U && feed_is_ip_address(token) &&
            remaining != NULL && *remaining != '\0') continue;
        ch_status status = feed_add_token(domains, cidrs, token, error);
        if (status == CH_OK) ++accepted;
        else if (status != CH_ERROR_NOT_FOUND) return status;
    }
    *usable = accepted > 0U;
    return CH_OK;
}

static ch_status feed_parse_adblock(char *line, feed_strings *domains,
                                    feed_strings *cidrs, int *usable,
                                    ch_error *error) {
    *usable = 1;
    line = feed_trim(line);
    if (*line == '\0' || *line == '!' || *line == '[' ||
        strncmp(line, "@@", 2U) == 0 || strstr(line, "##") != NULL ||
        strstr(line, "#@#") != NULL || strstr(line, "#$#") != NULL) {
        return CH_OK;
    }
    size_t length = strlen(line);
    if (length >= 2U && line[0] == '/' && line[length - 1U] == '/') {
        *usable = 0;
        return CH_OK;
    }
    char *options = strchr(line, '$');
    if (options != NULL) *options = '\0';
    if (strncmp(line, "||", 2U) == 0) {
        char *host = line + 2U;
        size_t cut = strcspn(host, "^/:?&|");
        host[cut] = '\0';
        char *domain = feed_normalize_domain(host);
        if (domain == NULL) {
            *usable = 0;
            return CH_OK;
        }
        ch_status status = feed_strings_add_unique(domains, domain, error);
        free(domain);
        return status;
    }
    if (strncmp(line, "|http://", 8U) == 0 ||
        strncmp(line, "|https://", 9U) == 0) {
        char *host = feed_url_hostname(line + 1U);
        char *domain = feed_normalize_domain(host);
        free(host);
        if (domain == NULL) {
            *usable = 0;
            return CH_OK;
        }
        ch_status status = feed_strings_add_unique(domains, domain, error);
        free(domain);
        return status;
    }
    return feed_parse_plain(line, 0, domains, cidrs, usable, error);
}

void ch_rule_feed_clear(ch_rule_feed *feed) {
    if (feed == NULL) return;
    free(feed->format);
    for (size_t index = 0U; index < feed->domain_suffix_count; ++index) {
        free(feed->domain_suffixes[index]);
    }
    free(feed->domain_suffixes);
    for (size_t index = 0U; index < feed->cidr_count; ++index) {
        free(feed->cidrs[index]);
    }
    free(feed->cidrs);
    memset(feed, 0, sizeof(*feed));
}

ch_status ch_rule_feed_parse(const char *body, size_t length,
                             const char *format, ch_rule_feed *out_feed,
                             ch_error *error) {
    ch_error_clear(error);
    if (body == NULL || out_feed == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed body and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_feed, 0, sizeof(*out_feed));
    if (length > CH_RULE_FEED_MAX_BYTES) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed exceeds %u bytes", CH_RULE_FEED_MAX_BYTES);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *document = malloc(length + 1U);
    if (document == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule feed body");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(document, body, length);
    document[length] = '\0';
    char normalized_format[8] = "auto";
    if (format != NULL) {
        while (*format != '\0' && isspace((unsigned char)*format)) ++format;
        size_t format_length = strlen(format);
        while (format_length > 0U &&
               isspace((unsigned char)format[format_length - 1U])) {
            --format_length;
        }
        if (format_length >= sizeof(normalized_format)) format_length = sizeof(normalized_format) - 1U;
        for (size_t index = 0U; index < format_length; ++index) {
            normalized_format[index] = (char)tolower((unsigned char)format[index]);
        }
        if (format_length > 0U) normalized_format[format_length] = '\0';
    }
    if (strcmp(normalized_format, "auto") == 0) {
        (void)snprintf(normalized_format, sizeof(normalized_format), "%s",
                       feed_detect_format(document));
    }
    if (strcmp(normalized_format, "plain") != 0 &&
        strcmp(normalized_format, "hosts") != 0 &&
        strcmp(normalized_format, "adblock") != 0) {
        free(document);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed format must be auto, plain, hosts, or adblock");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    feed_strings domains = {0};
    feed_strings cidrs = {0};
    ch_status status = CH_OK;
    char *save = NULL;
    for (char *line = strtok_r(document, "\n", &save); line != NULL;
         line = strtok_r(NULL, "\n", &save)) {
        line = feed_trim(line);
        if (strlen(line) >= 3U && (unsigned char)line[0] == 0xefU &&
            (unsigned char)line[1] == 0xbbU &&
            (unsigned char)line[2] == 0xbfU) line += 3U;
        if (*line == '\0') continue;
        size_t before = domains.count + cidrs.count;
        int usable = 0;
        if (strcmp(normalized_format, "adblock") == 0) {
            status = feed_parse_adblock(line, &domains, &cidrs, &usable,
                                        error);
        } else {
            status = feed_parse_plain(line,
                strcmp(normalized_format, "hosts") == 0,
                &domains, &cidrs, &usable, error);
        }
        if (status != CH_OK) break;
        if (!usable) ++out_feed->skipped;
        if (domains.count + cidrs.count > CH_RULE_FEED_MAX_ENTRIES ||
            domains.count + cidrs.count < before) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "rule feed has more than %u entries",
                         CH_RULE_FEED_MAX_ENTRIES);
            status = CH_ERROR_INVALID_ARGUMENT;
            break;
        }
    }
    free(document);
    if (status == CH_OK && domains.count + cidrs.count == 0U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "rule feed produced no usable entries");
        status = CH_ERROR_PARSE;
    }
    if (status != CH_OK) {
        feed_strings_clear(&domains);
        feed_strings_clear(&cidrs);
        ch_rule_feed_clear(out_feed);
        return status;
    }
    qsort(domains.items, domains.count, sizeof(*domains.items),
          feed_string_compare);
    qsort(cidrs.items, cidrs.count, sizeof(*cidrs.items),
          feed_string_compare);
    out_feed->format = ch_strdup(normalized_format);
    if (out_feed->format == NULL) {
        feed_strings_clear(&domains);
        feed_strings_clear(&cidrs);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy rule feed format");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    out_feed->domain_suffixes = domains.items;
    out_feed->domain_suffix_count = domains.count;
    out_feed->cidrs = cidrs.items;
    out_feed->cidr_count = cidrs.count;
    return CH_OK;
}

void ch_rule_feed_cache_clear(ch_rule_feed_cache *cache) {
    if (cache == NULL) return;
    ch_rule_feed_clear(&cache->feed);
    free(cache->profile);
    free(cache->name);
    free(cache->url);
    free(cache->action);
    for (size_t index = 0U; index < cache->network_count; ++index) {
        free(cache->networks[index]);
    }
    free(cache->networks);
    free(cache->etag);
    free(cache->last_modified);
    memset(cache, 0, sizeof(*cache));
}

static char *feed_safe_name(const char *raw) {
    size_t length = strlen(raw);
    char *result = malloc(length + 1U);
    if (result == NULL) return NULL;
    size_t used = 0U;
    for (size_t index = 0U; index < length; ++index) {
        unsigned char byte = (unsigned char)raw[index];
        char value = (char)tolower(byte);
        result[used++] = (char)(((value >= 'a' && value <= 'z') ||
            (value >= '0' && value <= '9') || value == '-' || value == '_') ?
            value : '-');
    }
    result[used] = '\0';
    char *start = result;
    while (*start == '-') ++start;
    char *end = start + strlen(start);
    while (end > start && end[-1] == '-') --end;
    *end = '\0';
    char *trimmed = ch_strdup(*start == '\0' ? "unnamed" : start);
    free(result);
    return trimmed;
}

static char *feed_cache_directory(const char *config_path,
                                  ch_rule_feed_kind kind) {
    const char *slash = strrchr(config_path, '/');
    size_t parent_length = slash == NULL ? 1U :
        (slash == config_path ? 1U : (size_t)(slash - config_path));
    const char *parent = slash == NULL ? "." : config_path;
    const char *suffix = kind == CH_RULE_FEED_RULE_SET ?
        "/.clambhook-rule-sets" : "/.clambhook-subscriptions";
    size_t suffix_length = strlen(suffix);
    char *result = malloc(parent_length + suffix_length + 1U);
    if (result == NULL) return NULL;
    memcpy(result, parent, parent_length);
    memcpy(result + parent_length, suffix, suffix_length + 1U);
    return result;
}

static char *feed_cache_path(const char *config_path, ch_rule_feed_kind kind,
                             const char *profile, const char *name,
                             const char *url, ch_error *error) {
    char *directory = feed_cache_directory(config_path, kind);
    char *safe_profile = feed_safe_name(profile);
    char *safe_feed = feed_safe_name(name);
    if (directory == NULL || safe_profile == NULL || safe_feed == NULL) {
        free(directory); free(safe_profile); free(safe_feed);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "construct rule feed cache path");
        return NULL;
    }
    feed_sha256 hash;
    unsigned char digest[32];
    static const char hex[] = "0123456789abcdef";
    char short_hash[13];
    const unsigned char zero = 0U;
    feed_sha256_init(&hash);
    feed_sha256_update(&hash, profile, strlen(profile));
    feed_sha256_update(&hash, &zero, 1U);
    feed_sha256_update(&hash, name, strlen(name));
    feed_sha256_update(&hash, &zero, 1U);
    feed_sha256_update(&hash, url, strlen(url));
    feed_sha256_final(&hash, digest);
    for (size_t index = 0U; index < 6U; ++index) {
        short_hash[index * 2U] = hex[digest[index] >> 4U];
        short_hash[index * 2U + 1U] = hex[digest[index] & 0x0fU];
    }
    short_hash[12] = '\0';
    int length = snprintf(NULL, 0, "%s/%s-%s-%s.json", directory,
                          safe_profile, safe_feed, short_hash);
    char *path = length < 0 ? NULL : malloc((size_t)length + 1U);
    if (path != NULL) {
        (void)snprintf(path, (size_t)length + 1U, "%s/%s-%s-%s.json",
                       directory, safe_profile, safe_feed, short_hash);
    } else {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate rule feed cache path");
    }
    free(directory); free(safe_profile); free(safe_feed);
    return path;
}

static int feed_append_json_strings(ch_json_buffer *json,
                                    char *const *items, size_t count) {
    if (!ch_json_append(json, "[")) return 0;
    for (size_t index = 0U; index < count; ++index) {
        if ((index == 0U || ch_json_append(json, ",")) &&
            ch_json_append_string(json, items[index])) continue;
        return 0;
    }
    return ch_json_append(json, "]");
}

ch_status ch_rule_feed_cache_write(const char *config_path,
                                   ch_rule_feed_kind kind,
                                   const ch_rule_feed_cache *cache,
                                   ch_error *error) {
    ch_error_clear(error);
    if (config_path == NULL || config_path[0] == '\0' || cache == NULL ||
        cache->profile == NULL || cache->name == NULL || cache->url == NULL ||
        cache->feed.format == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "complete rule feed cache metadata is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *directory = feed_cache_directory(config_path, kind);
    char *path = feed_cache_path(config_path, kind, cache->profile,
                                 cache->name, cache->url, error);
    if (directory == NULL || path == NULL) {
        free(directory); free(path);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "construct rule feed cache location");
        }
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    if (mkdir(directory, 0700) != 0 && errno != EEXIST) {
        ch_error_set(error, CH_ERROR_IO, "create rule feed cache directory: %s",
                     strerror(errno));
        free(directory); free(path);
        return CH_ERROR_IO;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"version\":1,\"profile\":") &&
        ch_json_append_string(&json, cache->profile) &&
        ch_json_append(&json, ",\"name\":") &&
        ch_json_append_string(&json, cache->name) &&
        ch_json_append(&json, ",\"url\":") &&
        ch_json_append_string(&json, cache->url) &&
        ch_json_append(&json, ",\"format\":") &&
        ch_json_append_string(&json, cache->feed.format);
    if (okay && kind == CH_RULE_FEED_SUBSCRIPTION) {
        okay = ch_json_append(&json, ",\"action\":") &&
            ch_json_append_string(&json,
                cache->action == NULL || cache->action[0] == '\0' ?
                    "block" : cache->action);
        if (okay && cache->network_count > 0U) {
            okay = ch_json_append(&json, ",\"networks\":") &&
                feed_append_json_strings(&json, cache->networks,
                                         cache->network_count);
        }
    }
    if (okay && cache->etag != NULL && cache->etag[0] != '\0') {
        okay = ch_json_append(&json, ",\"etag\":") &&
            ch_json_append_string(&json, cache->etag);
    }
    if (okay && cache->last_modified != NULL &&
        cache->last_modified[0] != '\0') {
        okay = ch_json_append(&json, ",\"last_modified\":") &&
            ch_json_append_string(&json, cache->last_modified);
    }
    if (okay) {
        okay = ch_json_append_format(
            &json, ",\"fetched_ts_ns\":%" PRId64,
            cache->fetched_ts_ns) &&
            (cache->feed.domain_suffix_count == 0U ||
                (ch_json_append(&json, ",\"domain_suffixes\":") &&
                 feed_append_json_strings(
                     &json, cache->feed.domain_suffixes,
                     cache->feed.domain_suffix_count))) &&
            (cache->feed.cidr_count == 0U ||
                (ch_json_append(&json, ",\"cidrs\":") &&
                 feed_append_json_strings(&json, cache->feed.cidrs,
                                          cache->feed.cidr_count))) &&
            ch_json_append_format(&json, ",\"skipped\":%zu}",
                                  cache->feed.skipped);
    }
    char *document = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (document == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode rule feed cache");
        free(directory); free(path);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    int temporary_size = snprintf(NULL, 0, "%s/.rule-feed-XXXXXX",
                                  directory);
    char *temporary = temporary_size < 0 ? NULL :
        malloc((size_t)temporary_size + 1U);
    if (temporary != NULL) {
        (void)snprintf(temporary, (size_t)temporary_size + 1U,
                       "%s/.rule-feed-XXXXXX", directory);
    }
    int descriptor = temporary == NULL ? -1 : mkstemp(temporary);
    ch_status status = CH_OK;
    if (descriptor < 0 || fchmod(descriptor, 0600) != 0) {
        ch_error_set(error, CH_ERROR_IO, "create rule feed cache: %s",
                     strerror(errno));
        status = CH_ERROR_IO;
    }
    size_t length = strlen(document);
    size_t written = 0U;
    while (status == CH_OK && written < length) {
        ssize_t count = write(descriptor, document + written,
                              length - written);
        if (count < 0 && errno == EINTR) continue;
        if (count <= 0) {
            ch_error_set(error, CH_ERROR_IO, "write rule feed cache: %s",
                         strerror(errno));
            status = CH_ERROR_IO;
        } else {
            written += (size_t)count;
        }
    }
    if (descriptor >= 0 && close(descriptor) != 0 && status == CH_OK) {
        ch_error_set(error, CH_ERROR_IO, "close rule feed cache: %s",
                     strerror(errno));
        status = CH_ERROR_IO;
    }
    if (status == CH_OK && rename(temporary, path) != 0) {
        ch_error_set(error, CH_ERROR_IO, "replace rule feed cache: %s",
                     strerror(errno));
        status = CH_ERROR_IO;
    }
    if (status != CH_OK && temporary != NULL) (void)unlink(temporary);
    free(document); free(temporary); free(directory); free(path);
    return status;
}

static ch_status feed_read_file(const char *path, char **out,
                                size_t *out_length, ch_error *error) {
    *out = NULL;
    FILE *file = fopen(path, "rb");
    if (file == NULL) return CH_ERROR_NOT_FOUND;
    if (fseek(file, 0L, SEEK_END) != 0) goto io_error;
    long end = ftell(file);
    if (end < 0L || (unsigned long)end > CH_RULE_FEED_MAX_BYTES + 65536UL) {
        (void)fclose(file);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed cache is too large");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (fseek(file, 0L, SEEK_SET) != 0) goto io_error;
    char *data = malloc((size_t)end + 1U);
    if (data == NULL) {
        (void)fclose(file);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate rule feed cache");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t read = fread(data, 1U, (size_t)end, file);
    if (read != (size_t)end || fclose(file) != 0) {
        free(data);
        ch_error_set(error, CH_ERROR_IO, "read rule feed cache %s", path);
        return CH_ERROR_IO;
    }
    data[read] = '\0';
    *out = data;
    *out_length = read;
    return CH_OK;
io_error:
    (void)fclose(file);
    ch_error_set(error, CH_ERROR_IO, "read rule feed cache %s", path);
    return CH_ERROR_IO;
}

static ch_status feed_copy_json_string(const ch_json_value *root,
                                       const char *key, char **out,
                                       int required, ch_error *error) {
    const ch_json_value *value = ch_json_object_get(root, key);
    const char *text = ch_json_string_value(value);
    if (text == NULL) {
        if (!required && value == NULL) {
            *out = ch_strdup("");
            return *out == NULL ? CH_ERROR_OUT_OF_MEMORY : CH_OK;
        }
        ch_error_set(error, CH_ERROR_PARSE,
                     "rule feed cache %s must be a string", key);
        return CH_ERROR_PARSE;
    }
    *out = ch_strdup(text);
    if (*out == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy rule feed cache %s", key);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static ch_status feed_load_json_strings(const ch_json_value *root,
                                        const char *key,
                                        feed_strings *out,
                                        int domains, ch_error *error) {
    const ch_json_value *array = ch_json_object_get(root, key);
    if (array == NULL) return CH_OK;
    if (ch_json_value_type(array) != CH_JSON_ARRAY) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "rule feed cache %s must be an array", key);
        return CH_ERROR_PARSE;
    }
    for (size_t index = 0U; index < ch_json_array_size(array); ++index) {
        const char *raw = ch_json_string_value(ch_json_array_get(array, index));
        if (raw == NULL) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "rule feed cache %s entries must be strings", key);
            return CH_ERROR_PARSE;
        }
        char *normalized = domains ? feed_normalize_domain(raw) :
            feed_normalize_cidr(raw);
        if (normalized == NULL) continue;
        ch_status status = feed_strings_add_unique(out, normalized, error);
        free(normalized);
        if (status != CH_OK) return status;
    }
    return CH_OK;
}

static ch_status feed_decode_cache(const char *json, size_t length,
                                   const char *profile, const char *name,
                                   const char *url,
                                   ch_rule_feed_cache *out,
                                   ch_error *error) {
    ch_json_value *root = ch_json_parse(json, length, error);
    if (root == NULL) return error->code;
    ch_status status = CH_OK;
    double version = ch_json_number_value(ch_json_object_get(root, "version"),
                                          0.0);
    if (ch_json_value_type(root) != CH_JSON_OBJECT || version != 1.0) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "rule feed cache version is unsupported");
        status = CH_ERROR_PARSE;
    }
    if (status == CH_OK) status = feed_copy_json_string(root, "profile",
                                                        &out->profile, 1,
                                                        error);
    if (status == CH_OK) status = feed_copy_json_string(root, "name",
                                                        &out->name, 1, error);
    if (status == CH_OK) status = feed_copy_json_string(root, "url",
                                                        &out->url, 1, error);
    if (status == CH_OK && (strcmp(out->profile, profile) != 0 ||
                            strcmp(out->name, name) != 0 ||
                            strcmp(out->url, url) != 0)) {
        status = CH_ERROR_NOT_FOUND;
    }
    if (status == CH_OK) status = feed_copy_json_string(root, "format",
                                                        &out->feed.format, 1,
                                                        error);
    if (status == CH_OK) status = feed_copy_json_string(root, "action",
                                                        &out->action, 0,
                                                        error);
    if (status == CH_OK) status = feed_copy_json_string(root, "etag",
                                                        &out->etag, 0, error);
    if (status == CH_OK) status = feed_copy_json_string(root, "last_modified",
                                                        &out->last_modified, 0,
                                                        error);
    feed_strings domains = {0};
    feed_strings cidrs = {0};
    feed_strings networks = {0};
    if (status == CH_OK) status = feed_load_json_strings(
        root, "domain_suffixes", &domains, 1, error);
    if (status == CH_OK) status = feed_load_json_strings(
        root, "cidrs", &cidrs, 0, error);
    const ch_json_value *network_array = ch_json_object_get(root, "networks");
    if (status == CH_OK && network_array != NULL) {
        if (ch_json_value_type(network_array) != CH_JSON_ARRAY) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "rule feed cache networks must be an array");
            status = CH_ERROR_PARSE;
        }
        for (size_t index = 0U; status == CH_OK &&
             index < ch_json_array_size(network_array); ++index) {
            const char *network = ch_json_string_value(
                ch_json_array_get(network_array, index));
            if (network == NULL) {
                ch_error_set(error, CH_ERROR_PARSE,
                             "rule feed cache networks must be strings");
                status = CH_ERROR_PARSE;
            } else {
                char lowered[16];
                size_t size = strlen(network);
                if (size >= sizeof(lowered)) size = sizeof(lowered) - 1U;
                for (size_t byte = 0U; byte < size; ++byte) {
                    lowered[byte] = (char)tolower((unsigned char)network[byte]);
                }
                lowered[size] = '\0';
                status = feed_strings_add_unique(&networks, lowered, error);
            }
        }
    }
    if (status == CH_OK) {
        const char *timestamp = strstr(json, "\"fetched_ts_ns\"");
        if (timestamp != NULL) timestamp = strchr(timestamp, ':');
        if (timestamp != NULL) {
            ++timestamp;
            while (*timestamp != '\0' &&
                   isspace((unsigned char)*timestamp)) ++timestamp;
            char *end = NULL;
            errno = 0;
            intmax_t parsed = strtoimax(timestamp, &end, 10);
            if (errno == 0 && end != timestamp && parsed >= INT64_MIN &&
                parsed <= INT64_MAX) {
                out->fetched_ts_ns = (int64_t)parsed;
            }
        }
        out->feed.skipped = (size_t)ch_json_number_value(
            ch_json_object_get(root, "skipped"), 0.0);
        qsort(domains.items, domains.count, sizeof(*domains.items),
              feed_string_compare);
        qsort(cidrs.items, cidrs.count, sizeof(*cidrs.items),
              feed_string_compare);
        qsort(networks.items, networks.count, sizeof(*networks.items),
              feed_string_compare);
        out->feed.domain_suffixes = domains.items;
        out->feed.domain_suffix_count = domains.count;
        out->feed.cidrs = cidrs.items;
        out->feed.cidr_count = cidrs.count;
        out->networks = networks.items;
        out->network_count = networks.count;
    } else {
        feed_strings_clear(&domains);
        feed_strings_clear(&cidrs);
        feed_strings_clear(&networks);
    }
    ch_json_value_destroy(root);
    if (status != CH_OK) ch_rule_feed_cache_clear(out);
    return status;
}

ch_status ch_rule_feed_cache_load(const char *config_path,
                                  ch_rule_feed_kind kind,
                                  const char *profile, const char *name,
                                  const char *url,
                                  ch_rule_feed_cache *out_cache,
                                  ch_error *error) {
    ch_error_clear(error);
    if (out_cache != NULL) memset(out_cache, 0, sizeof(*out_cache));
    if (config_path == NULL || config_path[0] == '\0') {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "rule feed cache not found");
        return CH_ERROR_NOT_FOUND;
    }
    if (profile == NULL || name == NULL || url == NULL ||
        out_cache == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed cache identity is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *directory = feed_cache_directory(config_path, kind);
    char *safe_profile = feed_safe_name(profile);
    char *safe_feed = feed_safe_name(name);
    if (directory == NULL || safe_profile == NULL || safe_feed == NULL) {
        free(directory); free(safe_profile); free(safe_feed);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "construct rule feed cache path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    int prefix_size = snprintf(NULL, 0, "%s-%s-", safe_profile, safe_feed);
    char *prefix = prefix_size < 0 ? NULL : malloc((size_t)prefix_size + 1U);
    if (prefix != NULL) {
        (void)snprintf(prefix, (size_t)prefix_size + 1U, "%s-%s-",
                       safe_profile, safe_feed);
    }
    free(safe_profile); free(safe_feed);
    if (prefix == NULL) {
        free(directory);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "construct rule feed cache prefix");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    DIR *stream = opendir(directory);
    if (stream == NULL) {
        free(directory); free(prefix);
        ch_error_set(error, CH_ERROR_NOT_FOUND, "rule feed cache not found");
        return CH_ERROR_NOT_FOUND;
    }
    ch_status status = CH_ERROR_NOT_FOUND;
    struct dirent *entry;
    while ((entry = readdir(stream)) != NULL) {
        size_t name_length = strlen(entry->d_name);
        size_t prefix_length = strlen(prefix);
        if (name_length <= prefix_length + 5U ||
            strncmp(entry->d_name, prefix, prefix_length) != 0 ||
            strcmp(entry->d_name + name_length - 5U, ".json") != 0) {
            continue;
        }
        int path_size = snprintf(NULL, 0, "%s/%s", directory,
                                 entry->d_name);
        char *path = path_size < 0 ? NULL : malloc((size_t)path_size + 1U);
        if (path == NULL) {
            status = CH_ERROR_OUT_OF_MEMORY;
            ch_error_set(error, status, "construct rule feed cache file");
            break;
        }
        (void)snprintf(path, (size_t)path_size + 1U, "%s/%s", directory,
                       entry->d_name);
        char *json = NULL;
        size_t length = 0U;
        status = feed_read_file(path, &json, &length, error);
        free(path);
        if (status == CH_OK) {
            status = feed_decode_cache(json, length, profile, name, url,
                                       out_cache, error);
        }
        free(json);
        if (status == CH_OK) break;
        if (status == CH_ERROR_NOT_FOUND) ch_error_clear(error);
        else break;
    }
    (void)closedir(stream);
    free(directory); free(prefix);
    if (status == CH_ERROR_NOT_FOUND) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "rule feed cache not found");
    }
    return status;
}

#ifndef __ANDROID__
typedef struct feed_http_buffer {
    char *data;
    size_t length;
    size_t capacity;
    int overflow;
} feed_http_buffer;

typedef struct feed_http_headers {
    char *etag;
    char *last_modified;
    char *location;
} feed_http_headers;

typedef struct feed_http_endpoint {
    char *url;
    char *scheme;
    char *host;
    char *port;
    char *resolve;
} feed_http_endpoint;

static pthread_once_t feed_curl_once = PTHREAD_ONCE_INIT;
static CURLcode feed_curl_init_status = CURLE_OK;

static void feed_curl_initialize(void) {
    feed_curl_init_status = curl_global_init(CURL_GLOBAL_DEFAULT);
}

static void feed_http_buffer_clear(feed_http_buffer *buffer) {
    free(buffer->data);
    memset(buffer, 0, sizeof(*buffer));
}

static void feed_http_headers_clear(feed_http_headers *headers) {
    free(headers->etag);
    free(headers->last_modified);
    free(headers->location);
    memset(headers, 0, sizeof(*headers));
}

static void feed_http_endpoint_clear(feed_http_endpoint *endpoint) {
    curl_free(endpoint->url);
    curl_free(endpoint->scheme);
    curl_free(endpoint->host);
    curl_free(endpoint->port);
    free(endpoint->resolve);
    memset(endpoint, 0, sizeof(*endpoint));
}

static int feed_ipv4_unsafe(const unsigned char address[4]) {
    return (address[0] == 0U && address[1] == 0U &&
            address[2] == 0U && address[3] == 0U) ||
        address[0] == 10U || address[0] == 127U ||
        (address[0] == 172U && address[1] >= 16U && address[1] <= 31U) ||
        (address[0] == 192U && address[1] == 168U) ||
        (address[0] == 169U && address[1] == 254U) ||
        (address[0] == 100U && address[1] >= 64U && address[1] <= 127U) ||
        (address[0] == 192U && address[1] == 0U && address[2] == 0U &&
         address[3] == 192U) || address[0] >= 224U;
}

static int feed_sockaddr_unsafe(const struct sockaddr *address) {
    if (address->sa_family == AF_INET) {
        const struct sockaddr_in *ipv4 = (const struct sockaddr_in *)address;
        return feed_ipv4_unsafe((const unsigned char *)&ipv4->sin_addr);
    }
    if (address->sa_family != AF_INET6) return 1;
    const struct sockaddr_in6 *ipv6 = (const struct sockaddr_in6 *)address;
    const unsigned char *bytes = (const unsigned char *)&ipv6->sin6_addr;
    static const unsigned char mapped[12] = {
        0U,0U,0U,0U,0U,0U,0U,0U,0U,0U,0xffU,0xffU
    };
    if (memcmp(bytes, mapped, sizeof(mapped)) == 0) {
        return feed_ipv4_unsafe(bytes + 12U);
    }
    int unspecified = 1;
    for (size_t index = 0U; index < 16U; ++index) {
        if (bytes[index] != 0U) unspecified = 0;
    }
    int loopback = unspecified == 0;
    for (size_t index = 0U; index < 15U; ++index) {
        if (bytes[index] != 0U) loopback = 0;
    }
    if (bytes[15] != 1U) loopback = 0;
    return unspecified || loopback || (bytes[0] & 0xfeU) == 0xfcU ||
        (bytes[0] == 0xfeU && (bytes[1] & 0xc0U) == 0x80U) ||
        bytes[0] == 0xffU;
}

static int feed_host_is_metadata(const char *host) {
    return strcasecmp(host, "metadata") == 0 ||
        strcasecmp(host, "instance-data") == 0 ||
        strcasecmp(host, "metadata.google.internal") == 0 ||
        strcasecmp(host, "metadata.azure.internal") == 0;
}

static int feed_host_is_localhost(const char *host) {
    size_t length = strlen(host);
    static const char suffix[] = ".localhost";
    return strcasecmp(host, "localhost") == 0 ||
        (length > sizeof(suffix) - 1U &&
         strcasecmp(host + length - (sizeof(suffix) - 1U), suffix) == 0);
}

static ch_status feed_http_prepare_endpoint(const char *url,
                                            feed_http_endpoint *out,
                                            ch_error *error) {
    memset(out, 0, sizeof(*out));
    CURLU *parsed = curl_url();
    if (parsed == NULL ||
        curl_url_set(parsed, CURLUPART_URL, url, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_SCHEME, &out->scheme, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_HOST, &out->host, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_PORT, &out->port,
                     CURLU_DEFAULT_PORT) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_URL, &out->url, 0U) != CURLUE_OK ||
        (strcasecmp(out->scheme, "http") != 0 &&
         strcasecmp(out->scheme, "https") != 0) || out->host[0] == '\0') {
        curl_url_cleanup(parsed);
        feed_http_endpoint_clear(out);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed URL must be http or https with a host");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    curl_url_cleanup(parsed);
    size_t host_length = strlen(out->host);
    while (host_length > 0U && out->host[host_length - 1U] == '.') {
        out->host[--host_length] = '\0';
    }
    if (feed_host_is_localhost(out->host) || feed_host_is_metadata(out->host)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed host %s is not public", out->host);
        feed_http_endpoint_clear(out);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    struct addrinfo *addresses = NULL;
    int resolved = getaddrinfo(out->host, out->port, &hints, &addresses);
    if (resolved != 0 || addresses == NULL) {
        ch_error_set(error, CH_ERROR_IO, "resolve rule feed host %s: %s",
                     out->host, gai_strerror(resolved));
        feed_http_endpoint_clear(out);
        return CH_ERROR_IO;
    }
    char numeric[NI_MAXHOST];
    numeric[0] = '\0';
    ch_status status = CH_OK;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        if (feed_sockaddr_unsafe(candidate->ai_addr)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "rule feed host %s resolves to a non-public address",
                         out->host);
            status = CH_ERROR_INVALID_ARGUMENT;
            break;
        }
        if (numeric[0] == '\0' && getnameinfo(
                candidate->ai_addr, candidate->ai_addrlen, numeric,
                sizeof(numeric), NULL, 0U, NI_NUMERICHOST) != 0) {
            ch_error_set(error, CH_ERROR_IO,
                         "format rule feed address for %s", out->host);
            status = CH_ERROR_IO;
            break;
        }
    }
    freeaddrinfo(addresses);
    if (status != CH_OK) {
        feed_http_endpoint_clear(out);
        return status;
    }
    int ipv6_host = strchr(out->host, ':') != NULL;
    int ipv6_address = strchr(numeric, ':') != NULL;
    int length = snprintf(NULL, 0,
        ipv6_host ? (ipv6_address ? "[%s]:%s:[%s]" : "[%s]:%s:%s") :
                    (ipv6_address ? "%s:%s:[%s]" : "%s:%s:%s"),
        out->host, out->port, numeric);
    out->resolve = length < 0 ? NULL : malloc((size_t)length + 1U);
    if (out->resolve != NULL) {
        (void)snprintf(out->resolve, (size_t)length + 1U,
            ipv6_host ? (ipv6_address ? "[%s]:%s:[%s]" : "[%s]:%s:%s") :
                        (ipv6_address ? "%s:%s:[%s]" : "%s:%s:%s"),
            out->host, out->port, numeric);
    } else {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate pinned rule feed address");
        feed_http_endpoint_clear(out);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static int feed_http_same_origin(const feed_http_endpoint *first,
                                 const feed_http_endpoint *next) {
    return strcasecmp(first->scheme, next->scheme) == 0 &&
        strcasecmp(first->host, next->host) == 0 &&
        strcmp(first->port, next->port) == 0;
}

static char *feed_http_resolve_redirect(const char *base,
                                        const char *location) {
    CURLU *parsed = curl_url();
    char *resolved = NULL;
    if (parsed != NULL &&
        curl_url_set(parsed, CURLUPART_URL, base, 0U) == CURLUE_OK &&
        curl_url_set(parsed, CURLUPART_URL, location, 0U) == CURLUE_OK) {
        (void)curl_url_get(parsed, CURLUPART_URL, &resolved, 0U);
    }
    curl_url_cleanup(parsed);
    return resolved;
}

static size_t feed_http_write(char *data, size_t size, size_t count,
                              void *context) {
    feed_http_buffer *buffer = context;
    if (size != 0U && count > SIZE_MAX / size) {
        buffer->overflow = 1;
        return 0U;
    }
    size_t length = size * count;
    if (length > CH_RULE_FEED_MAX_BYTES - buffer->length) {
        buffer->overflow = 1;
        return 0U;
    }
    size_t needed = buffer->length + length + 1U;
    if (needed > buffer->capacity) {
        size_t next = buffer->capacity == 0U ? 4096U : buffer->capacity;
        while (next < needed) {
            if (next > (CH_RULE_FEED_MAX_BYTES + 1U) / 2U) {
                next = needed;
                break;
            }
            next *= 2U;
        }
        char *grown = realloc(buffer->data, next);
        if (grown == NULL) return 0U;
        buffer->data = grown;
        buffer->capacity = next;
    }
    memcpy(buffer->data + buffer->length, data, length);
    buffer->length += length;
    buffer->data[buffer->length] = '\0';
    return length;
}

static int feed_http_set_header(char **target, const char *value,
                                size_t length) {
    while (length > 0U && (value[length - 1U] == '\r' ||
                           value[length - 1U] == '\n' ||
                           isspace((unsigned char)value[length - 1U]))) {
        --length;
    }
    while (length > 0U && isspace((unsigned char)*value)) {
        ++value;
        --length;
    }
    char *copy = malloc(length + 1U);
    if (copy == NULL) return 0;
    memcpy(copy, value, length);
    copy[length] = '\0';
    free(*target);
    *target = copy;
    return 1;
}

static size_t feed_http_header(char *data, size_t size, size_t count,
                               void *context) {
    feed_http_headers *headers = context;
    if (size != 0U && count > SIZE_MAX / size) return 0U;
    size_t length = size * count;
    const char *colon = memchr(data, ':', length);
    if (colon == NULL) return length;
    size_t name_length = (size_t)(colon - data);
    const char *value = colon + 1U;
    size_t value_length = length - name_length - 1U;
    int okay = 1;
    if (name_length == 4U && strncasecmp(data, "etag", 4U) == 0) {
        okay = feed_http_set_header(&headers->etag, value, value_length);
    } else if (name_length == 13U &&
               strncasecmp(data, "last-modified", 13U) == 0) {
        okay = feed_http_set_header(&headers->last_modified, value,
                                    value_length);
    } else if (name_length == 8U &&
               strncasecmp(data, "location", 8U) == 0) {
        okay = feed_http_set_header(&headers->location, value, value_length);
    }
    return okay ? length : 0U;
}

static int feed_http_add_request_header(struct curl_slist **headers,
                                        const char *name,
                                        const char *value) {
    int length = snprintf(NULL, 0, "%s: %s", name, value);
    char *line = length < 0 ? NULL : malloc((size_t)length + 1U);
    if (line == NULL) return 0;
    (void)snprintf(line, (size_t)length + 1U, "%s: %s", name, value);
    struct curl_slist *grown = curl_slist_append(*headers, line);
    free(line);
    if (grown == NULL) return 0;
    *headers = grown;
    return 1;
}

static int64_t feed_now_nanoseconds(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) return 0;
    return (int64_t)now.tv_sec * INT64_C(1000000000) +
        (int64_t)now.tv_nsec;
}

static ch_status feed_cache_copy_networks(
    ch_rule_feed_cache *cache, const ch_rule_feed_refresh_options *options,
    ch_error *error) {
    if (options->network_count == 0U) return CH_OK;
    cache->networks = calloc(options->network_count,
                             sizeof(*cache->networks));
    if (cache->networks == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate rule feed networks");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < options->network_count; ++index) {
        cache->networks[index] = ch_strdup(options->networks[index]);
        if (cache->networks[index] == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy rule feed network");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        ++cache->network_count;
    }
    qsort(cache->networks, cache->network_count,
          sizeof(*cache->networks), feed_string_compare);
    return CH_OK;
}

ch_status ch_rule_feed_refresh(const ch_rule_feed_refresh_options *options,
                               ch_error *error) {
    ch_error_clear(error);
    if (options == NULL || options->config_path == NULL ||
        options->profile == NULL || options->name == NULL ||
        options->url == NULL || options->format == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed refresh options are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    (void)pthread_once(&feed_curl_once, feed_curl_initialize);
    if (feed_curl_init_status != CURLE_OK) {
        ch_error_set(error, CH_ERROR_IO, "initialize HTTP client: %s",
                     curl_easy_strerror(feed_curl_init_status));
        return CH_ERROR_IO;
    }
    ch_rule_feed_cache old;
    ch_error old_error;
    ch_status old_status = ch_rule_feed_cache_load(
        options->config_path, options->kind, options->profile, options->name,
        options->url, &old, &old_error);
    char *current_url = ch_strdup(options->url);
    feed_http_endpoint initial = {0};
    feed_http_endpoint endpoint = {0};
    ch_status status = current_url == NULL ? CH_ERROR_OUT_OF_MEMORY :
        feed_http_prepare_endpoint(current_url, &initial, error);
    if (status == CH_OK) {
        status = feed_http_prepare_endpoint(current_url, &endpoint, error);
    }
    feed_http_buffer body = {0};
    feed_http_headers response_headers = {0};
    long http_status = 0L;
    for (unsigned redirect = 0U; status == CH_OK; ++redirect) {
        CURL *curl = curl_easy_init();
        struct curl_slist *resolve = NULL;
        struct curl_slist *headers = NULL;
        char curl_error[CURL_ERROR_SIZE] = {0};
        resolve = curl_slist_append(resolve, endpoint.resolve);
        int request_allocated = curl != NULL && resolve != NULL;
        if (old_status == CH_OK && old.etag != NULL && old.etag[0] != '\0') {
            request_allocated = request_allocated &&
                feed_http_add_request_header(&headers, "If-None-Match",
                                             old.etag);
        }
        if (old_status == CH_OK && old.last_modified != NULL &&
            old.last_modified[0] != '\0') {
            request_allocated = request_allocated &&
                feed_http_add_request_header(&headers, "If-Modified-Since",
                                             old.last_modified);
        }
        if (!request_allocated) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate rule feed HTTP request");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        feed_http_buffer_clear(&body);
        feed_http_headers_clear(&response_headers);
        CURLcode code = CURLE_OK;
#define FEED_CURL_SET(option, value) do { \
    if (status == CH_OK && code == CURLE_OK) \
        code = curl_easy_setopt(curl, (option), (value)); \
} while (0)
        FEED_CURL_SET(CURLOPT_URL, endpoint.url);
        FEED_CURL_SET(CURLOPT_HTTPHEADER, headers);
        FEED_CURL_SET(CURLOPT_RESOLVE, resolve);
        FEED_CURL_SET(CURLOPT_PROXY, "");
        FEED_CURL_SET(CURLOPT_PROTOCOLS_STR, "http,https");
        FEED_CURL_SET(CURLOPT_FOLLOWLOCATION, 0L);
        FEED_CURL_SET(CURLOPT_TIMEOUT_MS, 15000L);
        FEED_CURL_SET(CURLOPT_CONNECTTIMEOUT_MS, 15000L);
        FEED_CURL_SET(CURLOPT_NOSIGNAL, 1L);
        FEED_CURL_SET(CURLOPT_FRESH_CONNECT, 1L);
        FEED_CURL_SET(CURLOPT_FORBID_REUSE, 1L);
        FEED_CURL_SET(CURLOPT_DNS_CACHE_TIMEOUT, 0L);
        FEED_CURL_SET(CURLOPT_USERAGENT, "clambhook-c/1");
        FEED_CURL_SET(CURLOPT_WRITEFUNCTION, feed_http_write);
        FEED_CURL_SET(CURLOPT_WRITEDATA, &body);
        FEED_CURL_SET(CURLOPT_HEADERFUNCTION, feed_http_header);
        FEED_CURL_SET(CURLOPT_HEADERDATA, &response_headers);
        FEED_CURL_SET(CURLOPT_ERRORBUFFER, curl_error);
#undef FEED_CURL_SET
        if (status == CH_OK && code == CURLE_OK) code = curl_easy_perform(curl);
        if (status == CH_OK && code == CURLE_OK) {
            code = curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE,
                                     &http_status);
        }
        if (status == CH_OK && code != CURLE_OK) {
            if (body.overflow) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "rule feed body exceeds %u bytes",
                             CH_RULE_FEED_MAX_BYTES);
                status = CH_ERROR_INVALID_ARGUMENT;
            } else {
                ch_error_set(error, CH_ERROR_IO, "fetch rule feed: %s",
                    curl_error[0] == '\0' ? curl_easy_strerror(code) :
                                             curl_error);
                status = CH_ERROR_IO;
            }
        }
        curl_slist_free_all(headers);
        curl_slist_free_all(resolve);
        curl_easy_cleanup(curl);
        int is_redirect = http_status == 301L || http_status == 302L ||
            http_status == 303L || http_status == 307L ||
            http_status == 308L;
        if (status != CH_OK || !is_redirect) break;
        if (redirect >= 9U) {
            ch_error_set(error, CH_ERROR_IO,
                         "rule feed stopped after 10 redirects");
            status = CH_ERROR_IO;
            break;
        }
        if (response_headers.location == NULL ||
            response_headers.location[0] == '\0') {
            ch_error_set(error, CH_ERROR_IO,
                         "rule feed redirect has no location");
            status = CH_ERROR_IO;
            break;
        }
        char *next_url = feed_http_resolve_redirect(
            endpoint.url, response_headers.location);
        feed_http_endpoint next = {0};
        status = next_url == NULL ? CH_ERROR_INVALID_ARGUMENT :
            feed_http_prepare_endpoint(next_url, &next, error);
        curl_free(next_url);
        if (status == CH_OK && !feed_http_same_origin(&initial, &next)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "rule feed redirect to a different origin is not allowed");
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        if (status == CH_OK) {
            feed_http_endpoint_clear(&endpoint);
            endpoint = next;
            free(current_url);
            current_url = ch_strdup(endpoint.url);
            if (current_url == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy rule feed redirect URL");
                status = CH_ERROR_OUT_OF_MEMORY;
            }
        } else {
            feed_http_endpoint_clear(&next);
        }
    }
    if (status == CH_OK && http_status == 304L) {
        if (old_status != CH_OK) {
            ch_error_set(error, CH_ERROR_INVALID_STATE,
                         "rule feed was not modified without an existing cache");
            status = CH_ERROR_INVALID_STATE;
        } else {
            old.fetched_ts_ns = feed_now_nanoseconds();
            status = ch_rule_feed_cache_write(options->config_path,
                                               options->kind, &old, error);
        }
    } else if (status == CH_OK && (http_status < 200L || http_status >= 300L)) {
        ch_error_set(error, CH_ERROR_IO, "fetch rule feed: HTTP status %ld",
                     http_status);
        status = CH_ERROR_IO;
    } else if (status == CH_OK) {
        ch_rule_feed_cache cache;
        memset(&cache, 0, sizeof(cache));
        status = ch_rule_feed_parse(body.data == NULL ? "" : body.data,
                                    body.length, options->format,
                                    &cache.feed, error);
        if (status == CH_OK) {
            cache.profile = ch_strdup(options->profile);
            cache.name = ch_strdup(options->name);
            cache.url = ch_strdup(options->url);
            cache.action = ch_strdup(options->action == NULL ||
                                     options->action[0] == '\0' ? "block" :
                                     options->action);
            cache.etag = ch_strdup(response_headers.etag == NULL ? "" :
                                   response_headers.etag);
            cache.last_modified = ch_strdup(
                response_headers.last_modified == NULL ? "" :
                response_headers.last_modified);
            cache.fetched_ts_ns = feed_now_nanoseconds();
            if (cache.profile == NULL || cache.name == NULL ||
                cache.url == NULL || cache.action == NULL ||
                cache.etag == NULL || cache.last_modified == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "allocate rule feed cache metadata");
                status = CH_ERROR_OUT_OF_MEMORY;
            }
        }
        if (status == CH_OK) {
            status = feed_cache_copy_networks(&cache, options, error);
        }
        if (status == CH_OK) {
            status = ch_rule_feed_cache_write(options->config_path,
                                               options->kind, &cache, error);
        }
        ch_rule_feed_cache_clear(&cache);
    }
    if (old_status == CH_OK) ch_rule_feed_cache_clear(&old);
    feed_http_buffer_clear(&body);
    feed_http_headers_clear(&response_headers);
    feed_http_endpoint_clear(&endpoint);
    feed_http_endpoint_clear(&initial);
    free(current_url);
    return status;
}
#else
ch_status ch_rule_feed_refresh(const ch_rule_feed_refresh_options *options,
                               ch_error *error) {
    (void)options;
    ch_error_set(error, CH_ERROR_UNSUPPORTED,
                 "Android rule feed refresh is provided by Kotlin networking");
    return CH_ERROR_UNSUPPORTED;
}
#endif
