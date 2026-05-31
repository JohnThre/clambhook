package listener

import (
	"context"
	"errors"
	"net"

	"github.com/JohnThre/clambhook/internal/events"
)

const (
	RouteActionChain  = "chain"
	RouteActionDirect = "direct"
	RouteActionBlock  = "block"
	RouteActionReject = "reject"
)

var (
	ErrRouteBlocked  = errors.New("route blocked")
	ErrRouteRejected = errors.New("route rejected")
)

// RoutePlan is the listener-facing form of a routing decision. The planner
// owns the concrete chain/direct dialers; listeners only execute the plan.
type RoutePlan struct {
	RuleName   string
	Action     string
	ChainName  string
	Target     string
	Host       string
	Port       string
	Network    string
	ElapsedNs  int64
	Hops       []events.HopInfo
	Visibility events.VisibilityInfo

	Dial       func(context.Context, string, string) (net.Conn, error)
	DialPacket func(context.Context, string) (net.PacketConn, error)
}

// RoutePlanner decides how a listener should handle a target.
type RoutePlanner interface {
	Plan(ctx context.Context, network, target string) (RoutePlan, error)
	DefaultChainName() string
}
