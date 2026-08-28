// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.model;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class DashboardMapperTest {
    @Test
    void mapsDaemonContractsIntoSharedViewState() {
        DashboardData data = DashboardMapper.map(
                """
                        {"running":true,"profile":"default","listeners":[
                         {"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":2}]}
                        """,
                """
                        {"profiles":["default","work"],"active":"default"}
                        """,
                """
                        {"profile":"default","chains":[{"name":"proxy","servers":[
                         {"name":"edge","address":"example.test:443","protocol":"trojan",
                          "geo":{"city":"Singapore","country":"Singapore"}}]}]}
                        """,
                """
                        {"profile":"default","rules":[{"name":"private","action":"direct",
                         "cidrs":["10.0.0.0/8"],"networks":["tcp"]}]}
                        """,
                """
                        {"summary":{"active_connections":1,"rx_bps":2048,"tx_bps":1024,
                         "rx_total":8192,"tx_total":4096},"connections":[{
                         "conn_id":"c-1","target":"example.test:443","network":"tcp",
                         "state":"active","rule_action":"proxy","chain_name":"proxy",
                         "application":"browser","rx_bps":2048,"tx_bps":1024,
                         "rx_total":8192,"tx_total":4096}]}
                        """,
                """
                        {"enabled":true,"strategy":"route","upstreams":[{
                         "name":"secure","protocol":"doq","address":"dns.example:853"}]}
                        """,
                """
                        {"enabled":true,"capture_count":1}
                        """,
                """
                        {"entries":[{"id":"e-1","method":"GET","url":"https://example.test/",
                         "status_code":200,"error":""}]}
                        """
        );

        assertTrue(data.running());
        assertEquals("default", data.activeProfile());
        assertEquals(2, data.profiles().size());
        assertEquals(2, data.listeners().get(0).activeConnections());
        assertEquals("Singapore, Singapore", data.servers().get(0).location());
        assertEquals("CIDR: 10.0.0.0/8 · network: tcp", data.rules().get(0).matchSummary());
        assertEquals(1, data.traffic().activeConnections());
        assertEquals("example.test:443", data.connections().get(0).target());
        assertEquals("secure · DOQ · dns.example:853", data.dns().upstreams().get(0));
        assertEquals(200, data.developer().captures().get(0).status());
    }
}
