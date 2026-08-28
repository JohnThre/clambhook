// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/protocol.h"
#include "wireguard.h"

static const char *wireguard_test_key =
    "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=";

static ch_status wireguard_test_open(const char *private_key,
                                     const char *address,
                                     const char *allowed_ips,
                                     const char *log_level,
                                     int64_t keepalive,
                                     ch_error *error) {
    char document[2048];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"wireguard\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:51820\"\n"
        "protocol = \"wireguard\"\n"
        "[profile.chain.server.settings]\n"
        "private_key = \"%s\"\n"
        "addresses = [%s]\n"
        "dns = [\"1.1.1.1\"]\n"
        "mtu = 1280\n"
        "log_level = \"%s\"\n"
        "[[profile.chain.server.settings.peers]]\n"
        "public_key = \"%s\"\n"
        "endpoint = \"127.0.0.1:51820\"\n"
        "allowed_ips = [%s]\n"
        "persistent_keepalive = %lld\n",
        private_key, address, log_level, wireguard_test_key,
        allowed_ips, (long long)keepalive);
    ch_config *config = NULL;
    ch_status status = ch_config_parse(document, NULL, &config, error);
    if (status != CH_OK) return status;
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile,
                                                               "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    ch_packet_connection *packet = NULL;
    status = ch_protocol_chain_dial_packet(
        chain, "1.1.1.1:53", &packet, error);
    ch_packet_connection_close(packet);
    ch_protocol_reset_sessions();
    ch_config_free(config);
    return status;
}

static void wireguard_test_replay_window(void) {
    struct wireguard_keypair keypair;
    memset(&keypair, 0, sizeof(keypair));
    CH_TEST_ASSERT(wireguard_check_replay(&keypair, 0U));
    CH_TEST_ASSERT(wireguard_check_replay(&keypair, 2U));
    CH_TEST_ASSERT(wireguard_check_replay(&keypair, 1U));
    CH_TEST_ASSERT(!wireguard_check_replay(&keypair, 1U));
    CH_TEST_ASSERT(wireguard_check_replay(&keypair, 63U));
    CH_TEST_ASSERT(wireguard_check_replay(&keypair, 3U));
    CH_TEST_ASSERT(!wireguard_check_replay(&keypair, 0U));
    CH_TEST_ASSERT(wireguard_check_replay(&keypair, 64U));
    CH_TEST_ASSERT(!wireguard_check_replay(&keypair, 0U));
}

static int wireguard_test_send_all(int descriptor, const uint8_t *bytes,
                                   size_t length) {
    while (length > 0U) {
        ssize_t written = send(descriptor, bytes, length, 0);
        if (written <= 0) return 0;
        bytes += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int wireguard_test_receive_all(int descriptor, uint8_t *bytes,
                                      size_t length) {
    while (length > 0U) {
        ssize_t received = recv(descriptor, bytes, length, 0);
        if (received <= 0) return 0;
        bytes += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static void wireguard_test_external_roundtrip(void) {
    const char *endpoint = getenv("CLAMBHOOK_WG_ENDPOINT");
    const char *private_key = getenv("CLAMBHOOK_WG_CLIENT_PRIVATE");
    const char *server_public = getenv("CLAMBHOOK_WG_SERVER_PUBLIC");
    if (endpoint == NULL || private_key == NULL || server_public == NULL) {
        return;
    }
    char document[2048];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"wireguard\"\n"
        "[[profile.chain.server]]\n"
        "address = \"%s\"\nprotocol = \"wireguard\"\n"
        "[profile.chain.server.settings]\n"
        "private_key = \"%s\"\n"
        "addresses = [\"10.0.0.2/32\"]\n"
        "mtu = 1420\n"
        "[[profile.chain.server.settings.peers]]\n"
        "public_key = \"%s\"\nendpoint = \"%s\"\n"
        "allowed_ips = [\"10.0.0.0/24\"]\n",
        endpoint, private_key, server_public, endpoint);
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_table *chain = ch_config_array_get_table(
        ch_config_table_get_array(profile, "chain"), 0U);

    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_chain_dial(
        chain, "tcp", "10.0.0.1:9000", &stream, &error) == CH_OK);
    static const uint8_t tcp_payload[] = "native wireguard tcp";
    uint8_t tcp_echo[sizeof(tcp_payload)];
    CH_TEST_ASSERT(wireguard_test_send_all(
        stream, tcp_payload, sizeof(tcp_payload)));
    CH_TEST_ASSERT(wireguard_test_receive_all(
        stream, tcp_echo, sizeof(tcp_echo)));
    CH_TEST_ASSERT(memcmp(tcp_payload, tcp_echo, sizeof(tcp_payload)) == 0);
    (void)shutdown(stream, SHUT_RDWR);
    (void)close(stream);

    ch_packet_connection *packet = NULL;
    CH_TEST_ASSERT(ch_protocol_chain_dial_packet(
        chain, "10.0.0.1:9001", &packet, &error) == CH_OK);
    static const uint8_t udp_payload[] = "native wireguard udp";
    CH_TEST_ASSERT(ch_packet_connection_send(
        packet, "10.0.0.1:9001", udp_payload, sizeof(udp_payload),
        &error) == CH_OK);
    uint8_t udp_echo[128];
    size_t udp_length = 0U;
    char *source = NULL;
    CH_TEST_ASSERT(ch_packet_connection_receive_timeout(
        packet, udp_echo, sizeof(udp_echo), &udp_length, &source, 5000,
        &error) == CH_OK);
    CH_TEST_ASSERT(udp_length == sizeof(udp_payload));
    CH_TEST_ASSERT(memcmp(udp_payload, udp_echo, udp_length) == 0);
    CH_TEST_ASSERT_STRING("10.0.0.1:9001", source);
    free(source);
    ch_packet_connection_close(packet);
    ch_protocol_reset_sessions();
    ch_config_free(config);
}

void ch_test_wireguard(void) {
    ch_error error;
    ch_status status = wireguard_test_open(
        wireguard_test_key, "\"10.0.0.2/32\"",
        "\"0.0.0.0/0\", \"::/0\"", "silent", 25,
        &error);
    if (status != CH_OK) {
        fprintf(stderr, "wireguard valid config failed: %s\n", error.message);
    }
    CH_TEST_ASSERT(status == CH_OK);
    CH_TEST_ASSERT(wireguard_test_open(
        "bad-key", "\"10.0.0.2/32\"", "\"0.0.0.0/0\"",
        "error", 0, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "private_key") != NULL);
    CH_TEST_ASSERT(wireguard_test_open(
        wireguard_test_key, "\"not-a-cidr\"", "\"0.0.0.0/0\"",
        "error", 0, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "addresses") != NULL);
    CH_TEST_ASSERT(wireguard_test_open(
        wireguard_test_key, "\"10.0.0.2/32\"", "\"bad\"",
        "error", 0, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "allowed_ips") != NULL);
    CH_TEST_ASSERT(wireguard_test_open(
        wireguard_test_key, "\"10.0.0.2/32\"", "\"0.0.0.0/0\"",
        "noisy", 0, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "log_level") != NULL);
    CH_TEST_ASSERT(wireguard_test_open(
        wireguard_test_key, "\"10.0.0.2/32\"", "\"0.0.0.0/0\"",
        "error", -1, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "persistent_keepalive") != NULL);
    wireguard_test_replay_window();
    wireguard_test_external_roundtrip();
}
