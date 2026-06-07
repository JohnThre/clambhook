package listener

import (
	"context"
	"errors"
	"net"

	"github.com/JohnThre/clambhook/internal/events"
)

const (
	RouteActionChain  = "chain"
	RouteActionGroup  = "group"
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
	Profile    string
	RuleName   string
	Action     string
	ChainName  string
	GroupName  string
	Target     string
	Host       string
	Port       string
	Network    string
	Source     string
	Default    bool
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

// SourceRoutePlanner is implemented by planners that can use the client/source
// address as part of a routing decision.
type SourceRoutePlanner interface {
	RoutePlanner
	PlanWithSource(ctx context.Context, network, target, source string) (RoutePlan, error)
}

func PlanRoute(ctx context.Context, planner RoutePlanner, network, target, source string) (RoutePlan, error) {
	if sourcePlanner, ok := planner.(SourceRoutePlanner); ok {
		return sourcePlanner.PlanWithSource(ctx, network, target, source)
	}
	return planner.Plan(ctx, network, target)
}
