#include "clambhook/dns.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>

#if !defined(CLAMBHOOK_DNS_NO_DOH)
#include <curl/curl.h>
#endif
#include <openssl/err.h>
#include <openssl/ssl.h>

#include "internal.h"

#define CH_DNS_DEFAULT_TIMEOUT_MS 5000U

typedef enum ch_dns_upstream_kind {
    CH_DNS_UPSTREAM_DOH = 1,
    CH_DNS_UPSTREAM_DOT = 2
} ch_dns_upstream_kind;

typedef struct ch_dns_upstream {
    ch_dns_upstream_kind kind;
    char *name;
    char *url;
    char *effective_url;
    char *http_host;
    char *resolve_host;
    char *target;
    char *host;
    char *port;
    char *server_name;
    char **bootstrap_ips;
    size_t bootstrap_ip_count;
} ch_dns_upstream;

struct ch_dns_proxy {
    ch_dns_proxy_options options;
    unsigned int timeout_milliseconds;
    ch_dns_upstream *upstreams;
    size_t upstream_count;
};

#if !defined(CLAMBHOOK_DNS_NO_DOH)
typedef struct ch_dns_curl_response {
    uint8_t *bytes;
    size_t length;
    bool overflow;
} ch_dns_curl_response;

typedef struct ch_dns_curl_socket {
    ch_dns_proxy *proxy;
    ch_dns_upstream *upstream;
    ch_error error;
    bool failed;
} ch_dns_curl_socket;

static pthread_once_t ch_dns_curl_once = PTHREAD_ONCE_INIT;
static CURLcode ch_dns_curl_init_status = CURLE_OK;

static void ch_dns_curl_initialize(void) {
    ch_dns_curl_init_status = curl_global_init(CURL_GLOBAL_DEFAULT);
}
#endif

static char *ch_dns_optional_string(const ch_config_table *table,
                                    const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

static bool ch_dns_optional_bool(const ch_config_table *table, const char *key,
                                 bool fallback) {
    bool value = fallback;
    ch_error ignored;
    if (table != NULL) {
        (void)ch_config_table_get_bool(table, key, &value, &ignored);
    }
    return value;
}

static void ch_dns_trim_in_place(char *value) {
    if (value == NULL) return;
    char *start = value;
    while (isspace((unsigned char)*start)) ++start;
    if (start != value) memmove(value, start, strlen(start) + 1U);
    size_t length = strlen(value);
    while (length > 0U && isspace((unsigned char)value[length - 1U])) {
        value[--length] = '\0';
    }
}

static void ch_dns_lower_in_place(char *value) {
    if (value == NULL) return;
    for (; *value != '\0'; ++value) {
        *value = (char)tolower((unsigned char)*value);
    }
}

static bool ch_dns_is_ip(const char *host) {
    uint8_t address[sizeof(struct in6_addr)];
    return host != NULL &&
        (inet_pton(AF_INET, host, address) == 1 ||
         inet_pton(AF_INET6, host, address) == 1);
}

static char *ch_dns_join_host_port(const char *host, const char *port) {
    bool ipv6 = host != NULL && strchr(host, ':') != NULL;
    size_t host_length = host == NULL ? 0U : strlen(host);
    size_t port_length = port == NULL ? 0U : strlen(port);
    if (host_length > SIZE_MAX - port_length - 4U) return NULL;
    size_t capacity = host_length + port_length + (ipv6 ? 4U : 2U);
    char *target = malloc(capacity);
    if (target != NULL) {
        (void)snprintf(target, capacity, ipv6 ? "[%s]:%s" : "%s:%s",
                       host == NULL ? "" : host, port == NULL ? "" : port);
    }
    return target;
}

static bool ch_dns_split_host_port(const char *target, char **out_host,
                                   char **out_port) {
    *out_host = NULL;
    *out_port = NULL;
    if (target == NULL || target[0] == '\0') return false;
    const char *host_start = target;
    const char *host_end;
    const char *port_start;
    if (target[0] == '[') {
        const char *closing = strchr(target, ']');
        if (closing == NULL || closing[1] != ':' || closing[2] == '\0') {
            return false;
        }
        host_start = target + 1;
        host_end = closing;
        port_start = closing + 2;
    } else {
        const char *separator = strrchr(target, ':');
        if (separator == NULL || separator == target || separator[1] == '\0' ||
            memchr(target, ':', (size_t)(separator - target)) != NULL) {
            return false;
        }
        host_end = separator;
        port_start = separator + 1;
    }
    size_t host_length = (size_t)(host_end - host_start);
    *out_host = malloc(host_length + 1U);
    *out_port = ch_strdup(port_start);
    if (*out_host == NULL || *out_port == NULL) {
        free(*out_host);
        free(*out_port);
        *out_host = NULL;
        *out_port = NULL;
        return false;
    }
    memcpy(*out_host, host_start, host_length);
    (*out_host)[host_length] = '\0';
    return true;
}

static char *ch_dns_name_or_default(const char *name, const char *prefix,
                                    const char *host) {
    if (name != NULL && name[0] != '\0') return ch_strdup(name);
    size_t prefix_length = strlen(prefix);
    size_t host_length = host == NULL ? 0U : strlen(host);
    if (prefix_length > SIZE_MAX - host_length - 1U) return NULL;
    char *value = malloc(prefix_length + host_length + 1U);
    if (value != NULL) {
        memcpy(value, prefix, prefix_length);
        memcpy(value + prefix_length, host == NULL ? "" : host,
               host_length + 1U);
    }
    return value;
}

static void ch_dns_upstream_clear(ch_dns_upstream *upstream) {
    if (upstream == NULL) return;
    free(upstream->name);
    free(upstream->url);
    free(upstream->effective_url);
    free(upstream->http_host);
    free(upstream->resolve_host);
    free(upstream->target);
    free(upstream->host);
    free(upstream->port);
    free(upstream->server_name);
    for (size_t index = 0U; index < upstream->bootstrap_ip_count; ++index) {
        free(upstream->bootstrap_ips[index]);
    }
    free(upstream->bootstrap_ips);
    memset(upstream, 0, sizeof(*upstream));
}

static ch_status ch_dns_load_bootstrap(const ch_config_table *table,
                                       ch_dns_upstream *upstream,
                                       ch_error *error) {
    const ch_config_array *values = ch_config_table_get_array(
        table, "bootstrap_ips");
    size_t count = ch_config_array_count(values);
    if (count == 0U) return CH_OK;
    if (count > SIZE_MAX / sizeof(*upstream->bootstrap_ips)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "dns bootstrap list is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    upstream->bootstrap_ips = calloc(count,
                                     sizeof(*upstream->bootstrap_ips));
    if (upstream->bootstrap_ips == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate dns bootstrap list");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < count; ++index) {
        if (ch_config_array_get_string(values, index,
                                       &upstream->bootstrap_ips[index],
                                       error) != CH_OK) {
            upstream->bootstrap_ip_count = count;
            return error == NULL ? CH_ERROR_PARSE : error->code;
        }
        if (!ch_dns_is_ip(upstream->bootstrap_ips[index])) {
            upstream->bootstrap_ip_count = count;
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "dns bootstrap address %s is not an IP address",
                         upstream->bootstrap_ips[index]);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        ++upstream->bootstrap_ip_count;
    }
    return CH_OK;
}

static ch_status ch_dns_set_default_bootstrap(ch_dns_upstream *upstream,
                                              bool free_resolver,
                                              ch_error *error) {
    static const char *const custom[] = {
        "76.76.2.22", "76.76.10.22", "2606:1a40::22",
        "2606:1a40:1::22"
    };
    static const char *const free_addresses[] = {
        "76.76.2.11", "76.76.10.11", "2606:1a40::11",
        "2606:1a40:1::11"
    };
    const char *const *addresses = free_resolver ? free_addresses : custom;
    upstream->bootstrap_ips = calloc(4U,
                                     sizeof(*upstream->bootstrap_ips));
    if (upstream->bootstrap_ips == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Control D bootstrap list");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < 4U; ++index) {
        upstream->bootstrap_ips[index] = ch_strdup(addresses[index]);
        if (upstream->bootstrap_ips[index] == NULL) {
            upstream->bootstrap_ip_count = 4U;
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy Control D bootstrap address");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        ++upstream->bootstrap_ip_count;
    }
    return CH_OK;
}

static ch_status ch_dns_validate_direct_bootstrap(
    ch_dns_proxy *proxy,
    ch_dns_upstream *upstream,
    const char *network,
    ch_error *error) {
    if (ch_dns_is_ip(upstream->host) || upstream->bootstrap_ip_count > 0U) {
        return CH_OK;
    }
    ch_dns_route_action action = CH_DNS_ROUTE_CONNECT;
    ch_status status = proxy->options.route(
        network, upstream->target, &action, proxy->options.dial_context,
        error);
    if (status != CH_OK) return status;
    if (action == CH_DNS_ROUTE_DIRECT) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "direct DNS upstream %s needs bootstrap_ips to avoid "
                     "local DNS resolution", upstream->target);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return CH_OK;
}

static ch_status ch_dns_prepare_doh(ch_dns_proxy *proxy,
                                    ch_dns_upstream *upstream,
                                    ch_error *error) {
#if defined(CLAMBHOOK_DNS_NO_DOH)
    (void)proxy;
    (void)upstream;
    ch_error_set(error, CH_ERROR_UNSUPPORTED,
                 "native DNS-over-HTTPS is not linked on this platform");
    return CH_ERROR_UNSUPPORTED;
#else
    CURLU *parsed = curl_url();
    char *scheme = NULL;
    char *host = NULL;
    char *port = NULL;
    char *effective = NULL;
    if (parsed == NULL ||
        curl_url_set(parsed, CURLUPART_URL, upstream->url, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_SCHEME, &scheme, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_HOST, &host, 0U) != CURLUE_OK ||
        strcmp(scheme, "https") != 0) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns DoH requires a valid https URL");
        goto failure;
    }
    CURLUcode port_status = curl_url_get(parsed, CURLUPART_PORT, &port,
                                         CURLU_DEFAULT_PORT);
    if (port_status != CURLUE_OK || port == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns DoH URL has no port");
        goto failure;
    }
    upstream->host = ch_strdup(host);
    upstream->port = ch_strdup(port);
    upstream->target = ch_dns_join_host_port(host, port);
    if (upstream->server_name[0] == '\0' && !ch_dns_is_ip(host)) {
        free(upstream->server_name);
        upstream->server_name = ch_strdup(host);
    }
    const char *tls_host = upstream->server_name[0] == '\0' ? host :
        upstream->server_name;
    upstream->resolve_host = ch_strdup(tls_host);
    if (strcmp(tls_host, host) != 0) {
        if (curl_url_set(parsed, CURLUPART_HOST, tls_host, 0U) != CURLUE_OK) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "dns DoH server_name is invalid");
            goto failure;
        }
        size_t authority_capacity = strlen(host) + strlen(port) + 4U;
        upstream->http_host = malloc(authority_capacity);
        if (upstream->http_host != NULL) {
            bool ipv6 = strchr(host, ':') != NULL;
            bool default_port = strcmp(port, "443") == 0;
            (void)snprintf(
                upstream->http_host, authority_capacity,
                default_port ? (ipv6 ? "[%s]" : "%s") :
                               (ipv6 ? "[%s]:%s" : "%s:%s"),
                host, port);
        }
    }
    if (curl_url_get(parsed, CURLUPART_URL, &effective, 0U) != CURLUE_OK) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns DoH URL cannot be normalized");
        goto failure;
    }
    upstream->effective_url = ch_strdup(effective);
    if (upstream->host == NULL || upstream->port == NULL ||
        upstream->target == NULL || upstream->server_name == NULL ||
        upstream->resolve_host == NULL || upstream->effective_url == NULL ||
        (strcmp(tls_host, host) != 0 && upstream->http_host == NULL)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate dns DoH endpoint");
        goto failure;
    }
    curl_free(effective);
    curl_free(port);
    curl_free(host);
    curl_free(scheme);
    curl_url_cleanup(parsed);
    return ch_dns_validate_direct_bootstrap(proxy, upstream, "tcp", error);

failure:
    curl_free(effective);
    curl_free(port);
    curl_free(host);
    curl_free(scheme);
    curl_url_cleanup(parsed);
    return error == NULL || error->code == CH_OK ? CH_ERROR_INVALID_ARGUMENT :
                                                   error->code;
#endif
}

static ch_status ch_dns_prepare_dot(ch_dns_proxy *proxy,
                                    ch_dns_upstream *upstream,
                                    ch_error *error) {
    if (!ch_dns_split_host_port(upstream->target, &upstream->host,
                                &upstream->port)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns DoT address must be host:port");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (upstream->server_name[0] == '\0' && !ch_dns_is_ip(upstream->host)) {
        free(upstream->server_name);
        upstream->server_name = ch_strdup(upstream->host);
        if (upstream->server_name == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy dns DoT server name");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }
    return ch_dns_validate_direct_bootstrap(proxy, upstream, "tcp", error);
}

static ch_status ch_dns_expand_controld(const ch_config_table *table,
                                        char **protocol, char **url,
                                        char **target, char **server_name,
                                        char **name, bool *free_resolver,
                                        ch_error *error) {
    char *resolver = ch_dns_optional_string(table, "resolver");
    char *transport = ch_dns_optional_string(table, "transport");
    *free_resolver = ch_dns_optional_bool(table, "free", false);
    ch_dns_trim_in_place(resolver);
    ch_dns_trim_in_place(transport);
    ch_dns_lower_in_place(transport);
    if (resolver == NULL || transport == NULL) {
        free(resolver);
        free(transport);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Control D configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (resolver[0] == '\0' || strpbrk(resolver, " \t\r\n/") != NULL) {
        free(resolver);
        free(transport);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "controld resolver is required and must not contain "
                     "whitespace or '/'");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (transport[0] == '\0') {
        free(transport);
        transport = ch_strdup("doh");
    }
    if (transport == NULL ||
        (strcmp(transport, "doh") != 0 && strcmp(transport, "dot") != 0 &&
         strcmp(transport, "doq") != 0)) {
        free(resolver);
        free(transport);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "controld transport must be doh, dot, or doq");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host = *free_resolver ? "freedns.controld.com" :
                                        "dns.controld.com";
    *protocol = transport;
    if (strcmp(transport, "doh") == 0) {
        free(*url);
        *url = NULL;
        size_t capacity = strlen(host) + strlen(resolver) + 10U;
        *url = malloc(capacity);
        if (*url != NULL) {
            (void)snprintf(*url, capacity, "https://%s/%s", host, resolver);
        }
        if ((*server_name)[0] == '\0') {
            free(*server_name);
            *server_name = ch_strdup(host);
        }
    } else {
        free(*target);
        *target = NULL;
        size_t fqdn_capacity = strlen(resolver) + strlen(host) + 2U;
        char *fqdn = malloc(fqdn_capacity);
        if (fqdn != NULL) {
            (void)snprintf(fqdn, fqdn_capacity, "%s.%s", resolver, host);
            *target = ch_dns_join_host_port(fqdn, "853");
        }
        if ((*server_name)[0] == '\0') {
            free(*server_name);
            *server_name = fqdn == NULL ? NULL : ch_strdup(fqdn);
        }
        free(fqdn);
    }
    if ((*name)[0] == '\0') {
        free(*name);
        size_t capacity = strlen(resolver) + 10U;
        *name = malloc(capacity);
        if (*name != NULL) {
            (void)snprintf(*name, capacity, "controld:%s", resolver);
        }
    }
    free(resolver);
    if (*protocol == NULL || *name == NULL || *server_name == NULL ||
        (strcmp(*protocol, "doh") == 0 ? *url == NULL : *target == NULL)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Control D endpoint");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static ch_status ch_dns_upstream_load(ch_dns_proxy *proxy,
                                      const ch_config_table *table,
                                      ch_dns_upstream *upstream,
                                      ch_error *error) {
    memset(upstream, 0, sizeof(*upstream));
    char *protocol = ch_dns_optional_string(table, "protocol");
    char *name = ch_dns_optional_string(table, "name");
    char *url = ch_dns_optional_string(table, "url");
    char *target = ch_dns_optional_string(table, "address");
    char *server_name = ch_dns_optional_string(table, "server_name");
    if (protocol == NULL || name == NULL || url == NULL || target == NULL ||
        server_name == NULL) {
        free(protocol); free(name); free(url); free(target); free(server_name);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy dns upstream configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_dns_trim_in_place(protocol);
    ch_dns_lower_in_place(protocol);
    ch_dns_trim_in_place(name);
    ch_dns_trim_in_place(url);
    ch_dns_trim_in_place(target);
    ch_dns_trim_in_place(server_name);
    bool control_d = strcmp(protocol, "controld") == 0;
    bool free_resolver = false;
    if (control_d) {
        free(protocol);
        protocol = NULL;
        ch_status expanded = ch_dns_expand_controld(
            table, &protocol, &url, &target, &server_name, &name,
            &free_resolver, error);
        if (expanded != CH_OK) {
            free(protocol); free(name); free(url); free(target);
            free(server_name);
            return expanded;
        }
    }
    upstream->url = url;
    upstream->target = target;
    upstream->server_name = server_name;
    ch_status status = ch_dns_load_bootstrap(table, upstream, error);
    if (status == CH_OK && control_d && upstream->bootstrap_ip_count == 0U) {
        status = ch_dns_set_default_bootstrap(upstream, free_resolver, error);
    }
    if (status != CH_OK) {
        free(protocol); free(name);
        ch_dns_upstream_clear(upstream);
        return status;
    }
    if (strcmp(protocol, "doh") == 0) {
        upstream->kind = CH_DNS_UPSTREAM_DOH;
        status = ch_dns_prepare_doh(proxy, upstream, error);
        if (status == CH_OK) {
            upstream->name = ch_dns_name_or_default(name, "doh:",
                                                    upstream->host);
        }
    } else if (strcmp(protocol, "dot") == 0) {
        upstream->kind = CH_DNS_UPSTREAM_DOT;
        status = ch_dns_prepare_dot(proxy, upstream, error);
        if (status == CH_OK) {
            upstream->name = ch_dns_name_or_default(name, "dot:",
                                                    upstream->host);
        }
    } else if (strcmp(protocol, "doq") == 0) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "native DNS-over-QUIC is not available yet");
        status = CH_ERROR_UNSUPPORTED;
    } else {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "unknown dns protocol %s", protocol);
        status = CH_ERROR_INVALID_ARGUMENT;
    }
    free(protocol);
    free(name);
    if (status == CH_OK && upstream->name == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate dns upstream name");
        status = CH_ERROR_OUT_OF_MEMORY;
    }
    if (status != CH_OK) ch_dns_upstream_clear(upstream);
    return status;
}

size_t ch_dns_question_end(const uint8_t *message, size_t length) {
    if (message == NULL || length < CH_DNS_MIN_MESSAGE) return 0U;
    unsigned int question_count =
        (unsigned int)message[4] * 256U + (unsigned int)message[5];
    if (question_count == 0U) return 0U;
    size_t offset = CH_DNS_MIN_MESSAGE;
    for (unsigned int question = 0U; question < question_count; ++question) {
        for (;;) {
            if (offset >= length) return 0U;
            uint8_t label_length = message[offset++];
            if (label_length == 0U) break;
            if ((label_length & 0xc0U) != 0U || label_length > 63U ||
                (size_t)label_length > length - offset) {
                return 0U;
            }
            offset += (size_t)label_length;
        }
        if (length - offset < 4U) return 0U;
        offset += 4U;
    }
    return offset;
}

ch_status ch_dns_validate_response(const uint8_t *query, size_t query_length,
                                   const uint8_t *response,
                                   size_t response_length, ch_error *error) {
    ch_error_clear(error);
    size_t query_end = ch_dns_question_end(query, query_length);
    if (query_end == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns: malformed query");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (response == NULL || response_length < CH_DNS_MIN_MESSAGE) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "dns: upstream response too short");
        return CH_ERROR_PARSE;
    }
    if (query[0] != response[0] || query[1] != response[1]) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "dns: upstream response transaction ID mismatch");
        return CH_ERROR_PARSE;
    }
    size_t response_end = ch_dns_question_end(response, response_length);
    if (response_end == 0U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "dns: upstream response has malformed question");
        return CH_ERROR_PARSE;
    }
    size_t query_question_length = query_end - CH_DNS_MIN_MESSAGE;
    size_t response_question_length = response_end - CH_DNS_MIN_MESSAGE;
    if (query_question_length != response_question_length ||
        memcmp(query + CH_DNS_MIN_MESSAGE, response + CH_DNS_MIN_MESSAGE,
               query_question_length) != 0) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "dns: upstream response question mismatch");
        return CH_ERROR_PARSE;
    }
    return CH_OK;
}

ch_status ch_dns_servfail(const uint8_t *query, size_t query_length,
                          uint8_t **out_response,
                          size_t *out_response_length, ch_error *error) {
    ch_error_clear(error);
    if (out_response == NULL || out_response_length == NULL || query == NULL ||
        query_length < CH_DNS_MIN_MESSAGE) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns query and response outputs are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_response = NULL;
    *out_response_length = 0U;
    size_t end = ch_dns_question_end(query, query_length);
    if (end == 0U) end = CH_DNS_MIN_MESSAGE;
    uint8_t *response = malloc(end);
    if (response == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate dns SERVFAIL response");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(response, query, end);
    response[2] |= 0x80U;
    response[3] = (uint8_t)((response[3] & 0xf0U) | 0x02U);
    memset(response + 6U, 0, 6U);
    *out_response = response;
    *out_response_length = end;
    return CH_OK;
}

static void ch_dns_ssl_error(ch_error *error, const char *operation) {
    unsigned long code = ERR_get_error();
    char detail[192];
    if (code == 0UL) {
        (void)snprintf(detail, sizeof(detail), "unknown TLS error");
    } else {
        ERR_error_string_n(code, detail, sizeof(detail));
    }
    ch_error_set(error, CH_ERROR_IO, "dns %s: %s", operation, detail);
}

static bool ch_dns_ssl_write_all(SSL *ssl, const uint8_t *bytes,
                                 size_t length) {
    while (length > 0U) {
        int chunk = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int written = SSL_write(ssl, bytes, chunk);
        if (written <= 0) return false;
        bytes += (size_t)written;
        length -= (size_t)written;
    }
    return true;
}

static bool ch_dns_ssl_read_exact(SSL *ssl, uint8_t *bytes, size_t length) {
    while (length > 0U) {
        int chunk = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int received = SSL_read(ssl, bytes, chunk);
        if (received <= 0) return false;
        bytes += (size_t)received;
        length -= (size_t)received;
    }
    return true;
}

static void ch_dns_socket_timeout(int descriptor,
                                  unsigned int timeout_milliseconds) {
    struct timeval timeout = {
        .tv_sec = (time_t)(timeout_milliseconds / 1000U),
        .tv_usec = (suseconds_t)((timeout_milliseconds % 1000U) * 1000U)
    };
    (void)setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
    (void)setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
}

static bool ch_dns_load_trust_store(SSL_CTX *context) {
#if defined(__ANDROID__)
    if (SSL_CTX_load_verify_locations(
            context, NULL, "/system/etc/security/cacerts") == 1) {
        return true;
    }
    ERR_clear_error();
#endif
    return SSL_CTX_set_default_verify_paths(context) == 1;
}

static ch_status ch_dns_exchange_dot(ch_dns_proxy *proxy,
                                     ch_dns_upstream *upstream,
                                     const uint8_t *query,
                                     size_t query_length,
                                     uint8_t **out_response,
                                     size_t *out_response_length,
                                     ch_error *error) {
    int descriptor = -1;
    ch_status status = proxy->options.stream_dial(
        "tcp", upstream->target,
        (const char *const *)upstream->bootstrap_ips,
        upstream->bootstrap_ip_count, &descriptor,
        proxy->options.dial_context, error);
    if (status != CH_OK) return status;
    ch_dns_socket_timeout(descriptor, proxy->timeout_milliseconds);
    SSL_CTX *context = SSL_CTX_new(TLS_client_method());
    SSL *ssl = NULL;
    if (context == NULL || SSL_CTX_set_min_proto_version(
            context, TLS1_2_VERSION) != 1) {
        ch_dns_ssl_error(error, "create DoT context");
        status = CH_ERROR_IO;
        goto cleanup;
    }
    if (proxy->options.insecure_skip_verify) {
        SSL_CTX_set_verify(context, SSL_VERIFY_NONE, NULL);
    } else {
        SSL_CTX_set_verify(context, SSL_VERIFY_PEER, NULL);
        if (!ch_dns_load_trust_store(context)) {
            ch_dns_ssl_error(error, "load trust store");
            status = CH_ERROR_IO;
            goto cleanup;
        }
        if (upstream->server_name[0] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "dns DoT IP endpoint requires server_name for TLS "
                         "verification");
            status = CH_ERROR_INVALID_ARGUMENT;
            goto cleanup;
        }
    }
    ssl = SSL_new(context);
    if (ssl == NULL || SSL_set_fd(ssl, descriptor) != 1 ||
        (upstream->server_name[0] != '\0' &&
         (SSL_set_tlsext_host_name(ssl, upstream->server_name) != 1 ||
          (!proxy->options.insecure_skip_verify &&
           SSL_set1_host(ssl, upstream->server_name) != 1))) ||
        SSL_connect(ssl) != 1) {
        ch_dns_ssl_error(error, "DoT handshake");
        status = CH_ERROR_IO;
        goto cleanup;
    }
    uint8_t length_bytes[2] = {
        (uint8_t)(query_length >> 8U), (uint8_t)query_length
    };
    if (!ch_dns_ssl_write_all(ssl, length_bytes, sizeof(length_bytes)) ||
        !ch_dns_ssl_write_all(ssl, query, query_length) ||
        !ch_dns_ssl_read_exact(ssl, length_bytes, sizeof(length_bytes))) {
        ch_dns_ssl_error(error, "DoT exchange");
        status = CH_ERROR_IO;
        goto cleanup;
    }
    size_t response_length =
        (size_t)length_bytes[0] * 256U + (size_t)length_bytes[1];
    if (response_length < CH_DNS_MIN_MESSAGE) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "dns: upstream response too short");
        status = CH_ERROR_PARSE;
        goto cleanup;
    }
    uint8_t *response = malloc(response_length);
    if (response == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate DoT response");
        status = CH_ERROR_OUT_OF_MEMORY;
        goto cleanup;
    }
    if (!ch_dns_ssl_read_exact(ssl, response, response_length)) {
        free(response);
        ch_dns_ssl_error(error, "read DoT response");
        status = CH_ERROR_IO;
        goto cleanup;
    }
    status = ch_dns_validate_response(query, query_length, response,
                                      response_length, error);
    if (status == CH_OK) {
        *out_response = response;
        *out_response_length = response_length;
    } else {
        free(response);
    }

cleanup:
    if (ssl != NULL) {
        (void)SSL_shutdown(ssl);
        SSL_free(ssl);
    }
    SSL_CTX_free(context);
    (void)close(descriptor);
    return status;
}

#if !defined(CLAMBHOOK_DNS_NO_DOH)
static size_t ch_dns_curl_write(char *contents, size_t size, size_t count,
                                void *context) {
    ch_dns_curl_response *response = context;
    if (size != 0U && count > SIZE_MAX / size) {
        response->overflow = true;
        return 0U;
    }
    size_t incoming = size * count;
    if (incoming == 0U) return 0U;
    if (incoming > CH_DNS_MAX_MESSAGE - response->length) {
        response->overflow = true;
        return 0U;
    }
    uint8_t *next = realloc(response->bytes, response->length + incoming);
    if (next == NULL) return 0U;
    response->bytes = next;
    memcpy(response->bytes + response->length, contents, incoming);
    response->length += incoming;
    return incoming;
}

static curl_socket_t ch_dns_curl_open_socket(void *context,
                                             curlsocktype purpose,
                                             struct curl_sockaddr *address) {
    (void)address;
    ch_dns_curl_socket *socket = context;
    if (purpose != CURLSOCKTYPE_IPCXN) return CURL_SOCKET_BAD;
    int descriptor = -1;
    ch_status status = socket->proxy->options.stream_dial(
        "tcp", socket->upstream->target,
        (const char *const *)socket->upstream->bootstrap_ips,
        socket->upstream->bootstrap_ip_count, &descriptor,
        socket->proxy->options.dial_context, &socket->error);
    if (status != CH_OK || descriptor < 0) {
        socket->failed = true;
        if (descriptor >= 0) (void)close(descriptor);
        return CURL_SOCKET_BAD;
    }
    return (curl_socket_t)descriptor;
}

static int ch_dns_curl_socket_option(void *context, curl_socket_t descriptor,
                                     curlsocktype purpose) {
    (void)context;
    (void)descriptor;
    (void)purpose;
    return CURL_SOCKOPT_ALREADY_CONNECTED;
}

static char *ch_dns_curl_resolve_entry(const ch_dns_upstream *upstream) {
    const char *host = upstream->resolve_host;
    size_t capacity = strlen(host) + strlen(upstream->port) + 16U;
    char *entry = malloc(capacity);
    if (entry != NULL) {
        bool ipv6 = strchr(host, ':') != NULL;
        (void)snprintf(entry, capacity,
                       ipv6 ? "[%s]:%s:127.0.0.1" : "%s:%s:127.0.0.1",
                       host, upstream->port);
    }
    return entry;
}

static ch_status ch_dns_exchange_doh(ch_dns_proxy *proxy,
                                     ch_dns_upstream *upstream,
                                     const uint8_t *query,
                                     size_t query_length,
                                     uint8_t **out_response,
                                     size_t *out_response_length,
                                     ch_error *error) {
    (void)pthread_once(&ch_dns_curl_once, ch_dns_curl_initialize);
    if (ch_dns_curl_init_status != CURLE_OK) {
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize curl: %s",
                     curl_easy_strerror(ch_dns_curl_init_status));
        return CH_ERROR_INTERNAL;
    }
    CURL *curl = curl_easy_init();
    struct curl_slist *headers = NULL;
    struct curl_slist *resolve = NULL;
    ch_dns_curl_response response = {0};
    ch_dns_curl_socket socket = {
        .proxy = proxy,
        .upstream = upstream
    };
    char curl_error[CURL_ERROR_SIZE] = {0};
    char *resolve_entry = ch_dns_curl_resolve_entry(upstream);
    if (curl == NULL || resolve_entry == NULL) {
        curl_easy_cleanup(curl);
        free(resolve_entry);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate DoH request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    bool headers_ok = true;
    struct curl_slist *next_header = curl_slist_append(
        headers, "Content-Type: application/dns-message");
    if (next_header != NULL) {
        headers = next_header;
        next_header = curl_slist_append(headers,
                                        "Accept: application/dns-message");
        if (next_header != NULL) {
            headers = next_header;
        } else {
            headers_ok = false;
        }
    } else {
        headers_ok = false;
    }
    if (headers_ok && upstream->http_host != NULL) {
        size_t capacity = strlen(upstream->http_host) + 7U;
        char *host_header = malloc(capacity);
        if (host_header != NULL) {
            (void)snprintf(host_header, capacity, "Host: %s",
                           upstream->http_host);
            next_header = curl_slist_append(headers, host_header);
            if (next_header != NULL) {
                headers = next_header;
            } else {
                headers_ok = false;
            }
        } else {
            headers_ok = false;
        }
        free(host_header);
    }
    resolve = curl_slist_append(resolve, resolve_entry);
    free(resolve_entry);
    if (!headers_ok || resolve == NULL) {
        curl_slist_free_all(headers);
        curl_slist_free_all(resolve);
        curl_easy_cleanup(curl);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate DoH headers");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    CURLcode code = CURLE_OK;
#define CH_DNS_CURL_SET(option, value) do { \
    if (code == CURLE_OK) code = curl_easy_setopt(curl, (option), (value)); \
} while (0)
    CH_DNS_CURL_SET(CURLOPT_URL, upstream->effective_url);
    CH_DNS_CURL_SET(CURLOPT_POST, 1L);
    CH_DNS_CURL_SET(CURLOPT_POSTFIELDS, (const char *)query);
    CH_DNS_CURL_SET(CURLOPT_POSTFIELDSIZE_LARGE, (curl_off_t)query_length);
    CH_DNS_CURL_SET(CURLOPT_HTTPHEADER, headers);
    CH_DNS_CURL_SET(CURLOPT_HTTP_VERSION, CURL_HTTP_VERSION_2TLS);
    CH_DNS_CURL_SET(CURLOPT_SSLVERSION, CURL_SSLVERSION_TLSv1_2);
    CH_DNS_CURL_SET(CURLOPT_SSL_VERIFYPEER,
                    proxy->options.insecure_skip_verify ? 0L : 1L);
    CH_DNS_CURL_SET(CURLOPT_SSL_VERIFYHOST,
                    proxy->options.insecure_skip_verify ? 0L : 2L);
#if defined(__ANDROID__)
    CH_DNS_CURL_SET(CURLOPT_CAPATH, "/system/etc/security/cacerts");
#endif
    CH_DNS_CURL_SET(CURLOPT_TIMEOUT_MS, (long)proxy->timeout_milliseconds);
    CH_DNS_CURL_SET(CURLOPT_CONNECTTIMEOUT_MS,
                    (long)proxy->timeout_milliseconds);
    CH_DNS_CURL_SET(CURLOPT_NOSIGNAL, 1L);
    CH_DNS_CURL_SET(CURLOPT_FRESH_CONNECT, 1L);
    CH_DNS_CURL_SET(CURLOPT_FORBID_REUSE, 1L);
    CH_DNS_CURL_SET(CURLOPT_RESOLVE, resolve);
    CH_DNS_CURL_SET(CURLOPT_OPENSOCKETFUNCTION, ch_dns_curl_open_socket);
    CH_DNS_CURL_SET(CURLOPT_OPENSOCKETDATA, &socket);
    CH_DNS_CURL_SET(CURLOPT_SOCKOPTFUNCTION, ch_dns_curl_socket_option);
    CH_DNS_CURL_SET(CURLOPT_SOCKOPTDATA, &socket);
    CH_DNS_CURL_SET(CURLOPT_WRITEFUNCTION, ch_dns_curl_write);
    CH_DNS_CURL_SET(CURLOPT_WRITEDATA, &response);
    CH_DNS_CURL_SET(CURLOPT_ERRORBUFFER, curl_error);
#undef CH_DNS_CURL_SET
    if (code == CURLE_OK) code = curl_easy_perform(curl);
    long http_status = 0L;
    if (code == CURLE_OK) {
        code = curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &http_status);
    }
    ch_status status = CH_OK;
    if (code != CURLE_OK) {
        if (socket.failed && socket.error.code != CH_OK) {
            *error = socket.error;
            status = socket.error.code;
        } else if (response.overflow) {
            ch_error_set(error, CH_ERROR_PARSE, "DoH response too large");
            status = CH_ERROR_PARSE;
        } else {
            ch_error_set(error, CH_ERROR_IO, "DoH request: %s",
                         curl_error[0] == '\0' ? curl_easy_strerror(code) :
                                                 curl_error);
            status = CH_ERROR_IO;
        }
    } else if (http_status < 200L || http_status >= 300L) {
        ch_error_set(error, CH_ERROR_IO, "DoH HTTP status %ld", http_status);
        status = CH_ERROR_IO;
    } else {
        status = ch_dns_validate_response(query, query_length, response.bytes,
                                          response.length, error);
    }
    if (status == CH_OK) {
        *out_response = response.bytes;
        *out_response_length = response.length;
        response.bytes = NULL;
    }
    free(response.bytes);
    curl_slist_free_all(headers);
    curl_slist_free_all(resolve);
    curl_easy_cleanup(curl);
    return status;
}
#endif

ch_dns_proxy *ch_dns_proxy_create(const ch_config *config,
                                  const char *profile_name,
                                  const ch_dns_proxy_options *options,
                                  ch_error *error) {
    ch_error_clear(error);
    if (config == NULL || options == NULL || options->route == NULL ||
        options->stream_dial == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns config, route planner, and stream dialer are required");
        return NULL;
    }
    const ch_config_table *profile = profile_name == NULL ||
        profile_name[0] == '\0' ? ch_config_active_profile(config) :
                                  ch_config_profile_named(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "dns profile not found");
        return NULL;
    }
    const ch_config_table *dns = ch_config_table_get_table(profile, "dns");
    if (!ch_dns_optional_bool(dns, "enabled", false)) return NULL;
    ch_dns_proxy *proxy = calloc(1U, sizeof(*proxy));
    if (proxy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate dns proxy");
        return NULL;
    }
    proxy->options = *options;
    proxy->timeout_milliseconds = CH_DNS_DEFAULT_TIMEOUT_MS;
    char *timeout = ch_dns_optional_string(dns, "timeout");
    if (timeout == NULL) {
        ch_dns_proxy_destroy(proxy);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy dns timeout");
        return NULL;
    }
    if (timeout[0] != '\0') {
        int64_t nanoseconds = 0;
        if (ch_config_parse_duration_ns(timeout, &nanoseconds, error) != CH_OK ||
            nanoseconds < 0) {
            free(timeout);
            ch_dns_proxy_destroy(proxy);
            return NULL;
        }
        if (nanoseconds > 0) {
            uint64_t milliseconds =
                ((uint64_t)nanoseconds + UINT64_C(999999)) /
                UINT64_C(1000000);
            proxy->timeout_milliseconds = milliseconds > (uint64_t)UINT_MAX ?
                UINT_MAX : (unsigned int)milliseconds;
        }
    }
    free(timeout);
    const ch_config_array *upstreams = ch_config_table_get_array(dns,
                                                                  "upstream");
    size_t count = ch_config_array_count(upstreams);
    if (count == 0U || count > SIZE_MAX / sizeof(*proxy->upstreams)) {
        ch_dns_proxy_destroy(proxy);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns requires at least one upstream");
        return NULL;
    }
    proxy->upstreams = calloc(count, sizeof(*proxy->upstreams));
    if (proxy->upstreams == NULL) {
        ch_dns_proxy_destroy(proxy);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate dns upstreams");
        return NULL;
    }
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *upstream = ch_config_array_get_table(upstreams,
                                                                    index);
        ch_status status = ch_dns_upstream_load(
            proxy, upstream, &proxy->upstreams[index], error);
        if (status != CH_OK) {
            proxy->upstream_count = index + 1U;
            ch_dns_proxy_destroy(proxy);
            return NULL;
        }
        ++proxy->upstream_count;
    }
    return proxy;
}

void ch_dns_proxy_destroy(ch_dns_proxy *proxy) {
    if (proxy == NULL) return;
    for (size_t index = 0U; index < proxy->upstream_count; ++index) {
        ch_dns_upstream_clear(&proxy->upstreams[index]);
    }
    free(proxy->upstreams);
    free(proxy);
}

ch_status ch_dns_proxy_exchange(ch_dns_proxy *proxy, const uint8_t *query,
                                size_t query_length, uint8_t **out_response,
                                size_t *out_response_length,
                                ch_error *error) {
    ch_error_clear(error);
    if (proxy == NULL || query == NULL || out_response == NULL ||
        out_response_length == NULL || query_length > CH_DNS_MAX_MESSAGE ||
        ch_dns_question_end(query, query_length) == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dns: malformed query");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_response = NULL;
    *out_response_length = 0U;
    ch_status last_status = CH_ERROR_IO;
    ch_error last_error;
    ch_error_clear(&last_error);
    for (size_t index = 0U; index < proxy->upstream_count; ++index) {
        ch_dns_upstream *upstream = &proxy->upstreams[index];
        ch_error upstream_error;
        ch_status status;
#if defined(CLAMBHOOK_DNS_NO_DOH)
        if (upstream->kind == CH_DNS_UPSTREAM_DOH) {
            ch_error_set(&upstream_error, CH_ERROR_UNSUPPORTED,
                         "native DNS-over-HTTPS is not linked on this platform");
            status = CH_ERROR_UNSUPPORTED;
        } else {
            status = ch_dns_exchange_dot(
                proxy, upstream, query, query_length, out_response,
                out_response_length, &upstream_error);
        }
#else
        status = upstream->kind == CH_DNS_UPSTREAM_DOH ?
            ch_dns_exchange_doh(proxy, upstream, query, query_length,
                                out_response, out_response_length,
                                &upstream_error) :
            ch_dns_exchange_dot(proxy, upstream, query, query_length,
                                out_response, out_response_length,
                                &upstream_error);
#endif
        if (status == CH_OK) return CH_OK;
        last_status = status;
        last_error = upstream_error;
    }
    ch_error servfail_error;
    if (ch_dns_servfail(query, query_length, out_response,
                        out_response_length, &servfail_error) != CH_OK) {
        *error = servfail_error;
        return servfail_error.code;
    }
    ch_error_set(error, last_status, "%s: %s",
                 proxy->upstreams[proxy->upstream_count - 1U].name,
                 last_error.message);
    return last_status;
}

size_t ch_dns_proxy_upstream_count(const ch_dns_proxy *proxy) {
    return proxy == NULL ? 0U : proxy->upstream_count;
}

const char *ch_dns_proxy_upstream_name(const ch_dns_proxy *proxy,
                                       size_t index) {
    return proxy == NULL || index >= proxy->upstream_count ? NULL :
        proxy->upstreams[index].name;
}
