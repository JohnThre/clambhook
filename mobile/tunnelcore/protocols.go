package tunnelcore

import (
	"sort"

	"github.com/clambhook/clambhook/internal/protocol"

	// Register all daemon protocols for mobile tunnel use.
	_ "github.com/clambhook/clambhook/internal/protocol/openvpn"
	_ "github.com/clambhook/clambhook/internal/protocol/reality"
	_ "github.com/clambhook/clambhook/internal/protocol/shadowsocks"
	_ "github.com/clambhook/clambhook/internal/protocol/tor"
	_ "github.com/clambhook/clambhook/internal/protocol/trojan"
	_ "github.com/clambhook/clambhook/internal/protocol/vless"
	_ "github.com/clambhook/clambhook/internal/protocol/vmess"
	_ "github.com/clambhook/clambhook/internal/protocol/wireguard"
)

func SupportedProtocolsJSON() string {
	names := protocol.Registered()
	sort.Strings(names)
	return mustJSON(names)
}
