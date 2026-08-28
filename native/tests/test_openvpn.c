// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <errno.h>
#include <stdlib.h>

#include "clambhook/config.h"
#include "clambhook/protocol.h"
#include "protocol_openvpn.h"

static char *openvpn_test_read_file(const char *path) {
    FILE *file = fopen(path, "rb");
    if (file == NULL || fseek(file, 0, SEEK_END) != 0) {
        if (file != NULL) (void)fclose(file);
        return NULL;
    }
    long size = ftell(file);
    if (size < 0 || fseek(file, 0, SEEK_SET) != 0) {
        (void)fclose(file);
        return NULL;
    }
    char *contents = malloc((size_t)size + 1U);
    if (contents == NULL) {
        (void)fclose(file);
        return NULL;
    }
    size_t length = fread(contents, 1U, (size_t)size, file);
    int saved_error = ferror(file) ? errno : 0;
    (void)fclose(file);
    if (length != (size_t)size || saved_error != 0) {
        free(contents);
        return NULL;
    }
    contents[length] = '\0';
    return contents;
}

static char *openvpn_test_document(const char *endpoint, const char *ca,
                                   const char *certificate,
                                   const char *private_key,
                                   const char *cipher) {
    static const char prefix[] =
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"openvpn\"\n"
        "[[profile.chain.server]]\naddress = \"";
    static const char middle[] =
        "\"\nprotocol = \"openvpn\"\n"
        "[profile.chain.server.settings]\n"
        "server_cn = \"localhost\"\n"
        "cipher = \"";
    static const char cipher_suffix[] =
        "\"\ntun_mtu = 1500\nca_cert = '''\n";
    static const char certificate_label[] =
        "'''\nclient_cert = '''\n";
    static const char private_key_label[] =
        "'''\nclient_key = '''\n";
    static const char suffix[] = "'''\n";
    size_t total = sizeof(prefix) - 1U + strlen(endpoint) +
        sizeof(middle) - 1U + strlen(cipher) +
        sizeof(cipher_suffix) - 1U + strlen(ca) +
        sizeof(certificate_label) - 1U + strlen(certificate) +
        sizeof(private_key_label) - 1U + strlen(private_key) +
        sizeof(suffix);
    char *document = malloc(total);
    if (document == NULL) return NULL;
    (void)snprintf(document, total, "%s%s%s%s%s%s%s%s%s%s%s", prefix,
                   endpoint, middle, cipher, cipher_suffix, ca,
                   certificate_label, certificate, private_key_label,
                   private_key, suffix);
    return document;
}

static ch_status openvpn_test_rejected_config(const char *address,
                                               const char *settings,
                                               ch_error *error) {
    char document[4096];
    int length = snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"openvpn\"\n"
        "[[profile.chain.server]]\naddress = \"%s\"\n"
        "protocol = \"openvpn\"\n"
        "[profile.chain.server.settings]\n%s\n",
        address, settings);
    if (length < 0 || (size_t)length >= sizeof(document)) {
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_config *config = NULL;
    ch_status status = ch_config_parse(document, NULL, &config, error);
    if (status != CH_OK) return status;
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_table *chain = ch_config_array_get_table(
        ch_config_table_get_array(profile, "chain"), 0U);
    ch_packet_connection *packet = NULL;
    status = ch_protocol_chain_dial_packet(
        chain, "10.8.0.1:9", &packet, error);
    ch_packet_connection_close(packet);
    ch_protocol_reset_sessions();
    ch_config_free(config);
    return status;
}

static void openvpn_test_config_validation(void) {
    static const char complete[] =
        "ca_cert = \"not a PEM\"\n"
        "client_cert = \"not a PEM\"\n"
        "client_key = \"not a PEM\"";
    ch_error error;
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "", complete, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "address is required") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "missing-port", complete, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "invalid address") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "vpn.example:1194",
        "client_cert = \"certificate\"\nclient_key = \"key\"",
        &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "ca_cert is required") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "vpn.example:1194",
        "ca_cert = \"ca\"\nclient_cert = \"certificate\"",
        &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message,
                          "client_cert and client_key") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "vpn.example:1194",
        "ca_cert = \"ca\"\nclient_cert = \"certificate\"\n"
        "client_key = \"key\"\nusername = \"alice\"",
        &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message,
                          "username and password") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "vpn.example:1194",
        "ca_cert = \"ca\"\nclient_cert = \"certificate\"\n"
        "client_key = \"key\"\ncipher = \"AES-128-CBC\"",
        &error) == CH_ERROR_UNSUPPORTED);
    CH_TEST_ASSERT(strstr(error.message, "unsupported cipher") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "vpn.example:1194",
        "ca_cert = \"ca\"\nclient_cert = \"certificate\"\n"
        "client_key = \"key\"\ntun_mtu = 1279",
        &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "tun_mtu") != NULL);
    CH_TEST_ASSERT(openvpn_test_rejected_config(
        "vpn.example:1194", complete, &error) ==
        CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "valid PEM") != NULL);
}

static void openvpn_test_external_handshake(void) {
    const char *endpoint = getenv("CLAMBHOOK_OVPN_ENDPOINT");
    const char *ca_path = getenv("CLAMBHOOK_OVPN_CA");
    const char *certificate_path = getenv("CLAMBHOOK_OVPN_CERT");
    const char *private_key_path = getenv("CLAMBHOOK_OVPN_KEY");
    const char *cipher = getenv("CLAMBHOOK_OVPN_CIPHER");
    if (endpoint == NULL || ca_path == NULL || certificate_path == NULL ||
        private_key_path == NULL) return;

    char *ca = openvpn_test_read_file(ca_path);
    char *certificate = openvpn_test_read_file(certificate_path);
    char *private_key = openvpn_test_read_file(private_key_path);
    CH_TEST_ASSERT(ca != NULL && certificate != NULL && private_key != NULL);
    if (cipher == NULL || cipher[0] == '\0') cipher = "AES-256-GCM";
    char *document = openvpn_test_document(endpoint, ca, certificate,
                                            private_key, cipher);
    free(ca);
    free(certificate);
    free(private_key);
    CH_TEST_ASSERT(document != NULL);

    ch_error error;
    ch_config *config = NULL;
    ch_status status = ch_config_parse(document, NULL, &config, &error);
    free(document);
    if (status != CH_OK) {
        fprintf(stderr, "openvpn external config failed: %s\n",
                error.message);
    }
    CH_TEST_ASSERT(status == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_table *chain = ch_config_array_get_table(
        ch_config_table_get_array(profile, "chain"), 0U);
    ch_packet_connection *packet = NULL;
    status = ch_protocol_chain_dial_packet(
        chain, "10.8.0.1:9", &packet, &error);
    if (status != CH_OK) {
        fprintf(stderr, "openvpn external handshake failed: %s\n",
                error.message);
    }
    CH_TEST_ASSERT(status == CH_OK);
    static const uint8_t payload[] = "native openvpn udp";
    status = ch_packet_connection_send(packet, "10.8.0.1:9", payload,
                                       sizeof(payload), &error);
    if (status != CH_OK) {
        fprintf(stderr, "openvpn external data send failed: %s\n",
                error.message);
    }
    CH_TEST_ASSERT(status == CH_OK);
    ch_packet_connection_close(packet);
    ch_protocol_reset_sessions();
    ch_config_free(config);
}

void ch_test_openvpn(void) {
    ch_error error;
    ch_status status = ch_protocol_openvpn_test_fixtures(&error);
    if (status != CH_OK) {
        fprintf(stderr, "openvpn protocol fixtures failed: %s\n",
                error.message);
    }
    CH_TEST_ASSERT(status == CH_OK);
    openvpn_test_config_validation();
    openvpn_test_external_handshake();
}
