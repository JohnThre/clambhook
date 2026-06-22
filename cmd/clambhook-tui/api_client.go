//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type apiClient struct {
	baseURL    string
	wsBaseURL  string
	httpClient *http.Client
}

type networkInfoPayload struct {
	InterfaceName string `json:"interface_name,omitempty"`
	SSID          string `json:"ssid,omitempty"`
	IsWiFi        bool   `json:"is_wifi,omitempty"`
}

type statusPayload struct {
	Running     bool                    `json:"running"`
	Profile     string                  `json:"profile"`
	Listeners   []listenerStatusPayload `json:"listeners,omitempty"`
	TunnelMode  string                  `json:"tunnel_mode,omitempty"`
	NetworkInfo networkInfoPayload      `json:"network_info,omitempty"`
}

type listenerStatusPayload struct {
	Protocol    string `json:"protocol"`
	Addr        string `json:"addr"`
	ActiveConns int64  `json:"active_conns"`
}

type profilesPayload struct {
	Profiles []string `json:"profiles"`
	Active   string   `json:"active"`
}

type serversPayload struct {
	Profile string         `json:"profile"`
	Chains  []chainPayload `json:"chains"`
}

type policyGroupsPayload struct {
	Profile string               `json:"profile"`
	Groups  []policyGroupPayload `json:"groups"`
}

type policyGroupPayload struct {
	Name            string                     `json:"name"`
	Type            string                     `json:"type"`
	Chains          []string                   `json:"chains"`
	Selected        string                     `json:"selected,omitempty"`
	TestURL         string                     `json:"test_url"`
	Interval        string                     `json:"interval"`
	Timeout         string                     `json:"timeout"`
	SelectedChain   string                     `json:"selected_chain,omitempty"`
	SelectionMode   string                     `json:"selection_mode,omitempty"`
	SelectionReason string                     `json:"selection_reason,omitempty"`
	UpdatedTsNs     int64                      `json:"updated_ts_ns,omitempty"`
	Results         []policyProbeResultPayload `json:"results"`
}

type policyProbeResultPayload struct {
	ChainName    string `json:"chain_name"`
	Healthy      bool   `json:"healthy"`
	LatencyNs    int64  `json:"latency_ns,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	Error        string `json:"error,omitempty"`
	LastTestTsNs int64  `json:"last_test_ts_ns,omitempty"`
}

type policyGroupSelectionPayload struct {
	Profile      string               `json:"profile"`
	Groups       []policyGroupPayload `json:"groups"`
	PolicyGroups policyGroupsPayload  `json:"policy_groups"`
	Group        string               `json:"group"`
	Chain        string               `json:"chain"`
	BackupPath   string               `json:"backup_path"`
}

type chainPayload struct {
	Name         string                      `json:"name"`
	HopCount     int                         `json:"hop_count"`
	Capabilities protocolCapabilitiesPayload `json:"capabilities"`
	Servers      []serverPayload             `json:"servers"`
}

type serverPayload struct {
	Name         string                      `json:"name"`
	Address      string                      `json:"address"`
	Protocol     string                      `json:"protocol"`
	Capabilities protocolCapabilitiesPayload `json:"capabilities"`
	Geo          locationPayload             `json:"geo"`
	GeoError     string                      `json:"geo_error,omitempty"`
}

type protocolCapabilitiesPayload struct {
	TCP       bool   `json:"tcp"`
	UDP       bool   `json:"udp"`
	UDPMode   string `json:"udp_mode"`
	UDPReason string `json:"udp_reason,omitempty"`
}

type locationPayload struct {
	Country     string  `json:"country,omitempty"`
	CountryCode string  `json:"country_code,omitempty"`
	City        string  `json:"city,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type trafficSnapshotPayload struct {
	UpdatedTsNs        int64                      `json:"updated_ts_ns"`
	Summary            trafficSummaryPayload      `json:"summary"`
	Connections        []trafficConnectionPayload `json:"connections"`
	ProfileContext     profileContextPayload      `json:"profile_context,omitempty"`
	QuickFilters       []quickFilterPayload       `json:"quick_filters,omitempty"`
	RuleHits           []ruleHitPayload           `json:"rule_hits,omitempty"`
	BlockDecisions     []blockDecisionPayload     `json:"block_decisions,omitempty"`
	CleanupSuggestions []cleanupSuggestionPayload `json:"cleanup_suggestions,omitempty"`
	RuleSuggestions    []ruleSuggestionPayload    `json:"rule_suggestions,omitempty"`
	Breakdowns         trafficBreakdownsPayload   `json:"breakdowns,omitempty"`
	NetworkHierarchy   []appNodePayload           `json:"network_hierarchy,omitempty"`
}

type appNodePayload struct {
	Application string              `json:"application"`
	ConnCount   int                 `json:"conn_count"`
	ActiveCount int                 `json:"active_count"`
	RxTotal     uint64              `json:"rx_total"`
	TxTotal     uint64              `json:"tx_total"`
	Domains     []domainNodePayload `json:"domains,omitempty"`
}

type domainNodePayload struct {
	Domain    string               `json:"domain"`
	ConnCount int                  `json:"conn_count"`
	RxTotal   uint64               `json:"rx_total"`
	TxTotal   uint64               `json:"tx_total"`
	Countries []countryNodePayload `json:"countries,omitempty"`
}

type countryNodePayload struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ConnCount int    `json:"conn_count"`
	RxTotal   uint64 `json:"rx_total"`
	TxTotal   uint64 `json:"tx_total"`
}

type trafficSummaryPayload struct {
	ActiveConnections int     `json:"active_connections"`
	RxBps             float64 `json:"rx_bps"`
	TxBps             float64 `json:"tx_bps"`
	RxTotal           uint64  `json:"rx_total"`
	TxTotal           uint64  `json:"tx_total"`
	HistoryLimit      int     `json:"history_limit"`
	HistoryPath       string  `json:"history_path,omitempty"`
	HistoryPersisted  bool    `json:"history_persisted"`
	PersistError      string  `json:"persist_error,omitempty"`
}

type trafficConnectionPayload struct {
	ConnID       string              `json:"conn_id"`
	Profile      string              `json:"profile,omitempty"`
	State        string              `json:"state"`
	StartTsNs    int64               `json:"start_ts_ns"`
	UpdatedTsNs  int64               `json:"updated_ts_ns"`
	EndTsNs      int64               `json:"end_ts_ns,omitempty"`
	Listener     listenerInfo        `json:"listener"`
	ClientAddr   string              `json:"client_addr,omitempty"`
	ChainName    string              `json:"chain_name,omitempty"`
	GroupName    string              `json:"group_name,omitempty"`
	RuleName     string              `json:"rule_name,omitempty"`
	RuleAction   string              `json:"rule_action,omitempty"`
	Default      bool                `json:"default,omitempty"`
	DecisionNs   int64               `json:"decision_ns,omitempty"`
	Target       string              `json:"target,omitempty"`
	TargetHost   string              `json:"target_host,omitempty"`
	TargetPort   string              `json:"target_port,omitempty"`
	Network      string              `json:"network,omitempty"`
	Source       string              `json:"source,omitempty"`
	Application  string              `json:"application,omitempty"`
	Hops         []trafficHop        `json:"hops,omitempty"`
	Timeline     []timelineEvent     `json:"timeline,omitempty"`
	Visibility   *visibilityInfo     `json:"visibility,omitempty"`
	RouteControl routeControlPayload `json:"route_control,omitempty"`
	Geo          locationPayload     `json:"geo"`
	GeoError     string              `json:"geo_error,omitempty"`
	TotalDialNs  int64               `json:"total_dial_ns,omitempty"`
	RxBps        float64             `json:"rx_bps"`
	TxBps        float64             `json:"tx_bps"`
	RxTotal      uint64              `json:"rx_total"`
	TxTotal      uint64              `json:"tx_total"`
	DurationNs   int64               `json:"duration_ns,omitempty"`
	CloseReason  string              `json:"close_reason,omitempty"`
}

type routeControlPayload struct {
	Mode            string `json:"mode,omitempty"`
	Decision        string `json:"decision,omitempty"`
	Source          string `json:"source,omitempty"`
	RuleName        string `json:"rule_name,omitempty"`
	RuleNumber      int    `json:"rule_number,omitempty"`
	PolicyGroup     string `json:"policy_group,omitempty"`
	SelectedChain   string `json:"selected_chain,omitempty"`
	SelectionReason string `json:"selection_reason,omitempty"`
	Fallback        bool   `json:"fallback,omitempty"`
	Default         bool   `json:"default,omitempty"`
}

type profileContextPayload struct {
	Active   string   `json:"active,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
}

type trafficBreakdownsPayload struct {
	Profiles []breakdownRowPayload `json:"profiles,omitempty"`
	Chains   []breakdownRowPayload `json:"chains,omitempty"`
	Rules    []breakdownRowPayload `json:"rules,omitempty"`
	Actions  []breakdownRowPayload `json:"actions,omitempty"`
	Networks []breakdownRowPayload `json:"networks,omitempty"`
}

type breakdownRowPayload struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Count   int    `json:"count"`
	RxTotal uint64 `json:"rx_total"`
	TxTotal uint64 `json:"tx_total"`
}

type quickFilterPayload struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type ruleHitPayload struct {
	Profile     string `json:"profile,omitempty"`
	RuleName    string `json:"rule_name"`
	Action      string `json:"action"`
	Count       int    `json:"count"`
	LastHitTsNs int64  `json:"last_hit_ts_ns,omitempty"`
	RxTotal     uint64 `json:"rx_total"`
	TxTotal     uint64 `json:"tx_total"`
	LastTarget  string `json:"last_target,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Temporary   bool   `json:"temporary,omitempty"`
}

type blockDecisionPayload struct {
	ConnID      string `json:"conn_id"`
	Profile     string `json:"profile,omitempty"`
	RuleName    string `json:"rule_name,omitempty"`
	Action      string `json:"action"`
	Target      string `json:"target,omitempty"`
	TargetHost  string `json:"target_host,omitempty"`
	TargetPort  string `json:"target_port,omitempty"`
	Network     string `json:"network,omitempty"`
	TsNs        int64  `json:"ts_ns"`
	CloseReason string `json:"close_reason,omitempty"`
}

type cleanupSuggestionPayload struct {
	Kind           string `json:"kind"`
	Profile        string `json:"profile,omitempty"`
	RuleName       string `json:"rule_name"`
	TargetRuleName string `json:"target_rule_name,omitempty"`
	Operation      string `json:"operation,omitempty"`
	Action         string `json:"action,omitempty"`
	Message        string `json:"message"`
	Count          int    `json:"count,omitempty"`
	LastHitTsNs    int64  `json:"last_hit_ts_ns,omitempty"`
}

type ruleSuggestionPayload struct {
	ID            string      `json:"id"`
	Kind          string      `json:"kind"`
	Profile       string      `json:"profile,omitempty"`
	Action        string      `json:"action"`
	DraftRule     rulePayload `json:"draft_rule"`
	Count         int         `json:"count"`
	LastSeenTsNs  int64       `json:"last_seen_ts_ns,omitempty"`
	SampleTargets []string    `json:"sample_targets,omitempty"`
	Confidence    string      `json:"confidence,omitempty"`
	Reason        string      `json:"reason,omitempty"`
}

type developerStatusPayload struct {
	Enabled               bool   `json:"enabled"`
	MITMEnabled           bool   `json:"mitm_enabled"`
	NoCacheEnabled        bool   `json:"no_cache_enabled"`
	CaptureLimit          int    `json:"capture_limit"`
	BodyLimitBytes        int64  `json:"body_limit_bytes"`
	HeaderValueLimitBytes int    `json:"header_value_limit_bytes"`
	CACertPath            string `json:"ca_cert_path,omitempty"`
	CAFingerprintSHA256   string `json:"ca_fingerprint_sha256,omitempty"`
	CANotBefore           string `json:"ca_not_before,omitempty"`
	CANotAfter            string `json:"ca_not_after,omitempty"`
	CaptureCount          int    `json:"capture_count"`
}

type developerEntriesPayload struct {
	Entries []developerEntryPayload `json:"entries"`
}

type developerEntryPayload struct {
	ID         string                  `json:"id"`
	ConnID     string                  `json:"conn_id,omitempty"`
	Profile    string                  `json:"profile,omitempty"`
	ClientAddr string                  `json:"client_addr,omitempty"`
	ChainName  string                  `json:"chain_name,omitempty"`
	Method     string                  `json:"method"`
	URL        string                  `json:"url"`
	Scheme     string                  `json:"scheme"`
	Host       string                  `json:"host"`
	Status     int                     `json:"status,omitempty"`
	Request    developerMessagePayload `json:"request"`
	Response   developerMessagePayload `json:"response"`
	Error      string                  `json:"error,omitempty"`
}

type developerMessagePayload struct {
	Headers []developerHeaderPayload `json:"headers,omitempty"`
	Cookies []developerCookiePayload `json:"cookies,omitempty"`
	Body    developerBodyPayload     `json:"body"`
}

type developerHeaderPayload struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Redacted  bool   `json:"redacted,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type developerBodyPayload struct {
	Size           int64  `json:"size"`
	Preview        string `json:"preview,omitempty"`
	PreviewBase64  string `json:"preview_base64,omitempty"`
	PreviewBytes   int64  `json:"preview_bytes"`
	Truncated      bool   `json:"truncated"`
	TruncatedAfter int64  `json:"truncated_after"`
	MimeType       string `json:"mime_type,omitempty"`
	Encoding       string `json:"encoding,omitempty"`
}

type developerCookiePayload struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  string `json:"expires,omitempty"`
	MaxAge   int    `json:"max_age,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"http_only,omitempty"`
	SameSite string `json:"same_site,omitempty"`
}

type rulePayload struct {
	Name           string   `json:"name"`
	Action         string   `json:"action"`
	RuleSets       []string `json:"rule_sets,omitempty"`
	Domains        []string `json:"domains,omitempty"`
	DomainSuffixes []string `json:"domain_suffixes,omitempty"`
	DomainKeywords []string `json:"domain_keywords,omitempty"`
	CIDRs          []string `json:"cidrs,omitempty"`
	SourceCIDRs    []string `json:"source_cidrs,omitempty"`
	Ports          []int    `json:"ports,omitempty"`
	Networks       []string `json:"networks,omitempty"`
}

type createRuleRequest struct {
	Profile  string      `json:"profile,omitempty"`
	Rule     rulePayload `json:"rule"`
	Position string      `json:"position,omitempty"`
}

type createRuleFromConnectionRequest struct {
	ConnID   string `json:"conn_id"`
	Profile  string `json:"profile,omitempty"`
	Name     string `json:"name,omitempty"`
	Action   string `json:"action,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Position string `json:"position,omitempty"`
}

type cleanupRuleRequest struct {
	Profile        string `json:"profile,omitempty"`
	Kind           string `json:"kind"`
	RuleName       string `json:"rule_name"`
	TargetRuleName string `json:"target_rule_name"`
	Operation      string `json:"operation"`
}

type ruleTestRequest struct {
	Profile string `json:"profile,omitempty"`
	Network string `json:"network"`
	Target  string `json:"target"`
}

type ruleTestResponse struct {
	Profile  string                  `json:"profile"`
	Decision ruleTestDecisionPayload `json:"decision"`
	Chain    *ruleTestChainPayload   `json:"chain,omitempty"`
	Hops     []serverPayload         `json:"hops,omitempty"`
}

type ruleTestDecisionPayload struct {
	RuleName  string `json:"rule_name,omitempty"`
	Action    string `json:"action"`
	ChainName string `json:"chain_name,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	Target    string `json:"target"`
	Host      string `json:"target_host,omitempty"`
	Port      string `json:"target_port,omitempty"`
	Network   string `json:"network,omitempty"`
	Source    string `json:"source,omitempty"`
	Default   bool   `json:"default,omitempty"`
	ElapsedNs int64  `json:"elapsed_ns,omitempty"`
}

type ruleTestChainPayload struct {
	Name         string                      `json:"name"`
	HopCount     int                         `json:"hop_count"`
	Capabilities protocolCapabilitiesPayload `json:"capabilities"`
}

type listenerInfo struct {
	Protocol string `json:"protocol"`
	Addr     string `json:"addr"`
}

type visibilityInfo struct {
	Kind      string `json:"kind,omitempty"`
	Method    string `json:"method,omitempty"`
	Scheme    string `json:"scheme,omitempty"`
	Host      string `json:"host,omitempty"`
	Port      string `json:"port,omitempty"`
	Path      string `json:"path,omitempty"`
	QueryType string `json:"query_type,omitempty"`
}

type timelineEvent struct {
	TsNs   int64  `json:"ts_ns"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

type trafficHop struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Address   string `json:"address,omitempty"`
	State     string `json:"state,omitempty"`
	ElapsedNs int64  `json:"elapsed_ns,omitempty"`
	Error     string `json:"error,omitempty"`
}

func newAPIClient(apiAddr string) apiClient {
	if strings.HasPrefix(apiAddr, "http://") || strings.HasPrefix(apiAddr, "https://") {
		return newAPIClientFromBaseURL(apiAddr)
	}
	return newAPIClientFromBaseURL("http://" + apiAddr)
}

func newAPIClientFromBaseURL(raw string) apiClient {
	base := strings.TrimRight(raw, "/")
	wsBase := strings.TrimPrefix(base, "http://")
	wsBase = strings.TrimPrefix(wsBase, "https://")
	scheme := "ws://"
	if strings.HasPrefix(base, "https://") {
		scheme = "wss://"
	}
	return apiClient{
		baseURL:   base,
		wsBaseURL: scheme + wsBase,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c apiClient) status() (statusPayload, error) {
	var out statusPayload
	err := c.getJSON("/api/v1/status", &out)
	return out, err
}

func (c apiClient) profiles() (profilesPayload, error) {
	var out profilesPayload
	err := c.getJSON("/api/v1/profiles", &out)
	return out, err
}

func (c apiClient) servers() (serversPayload, error) {
	var out serversPayload
	err := c.getJSON("/api/v1/servers", &out)
	return out, err
}

func (c apiClient) policyGroups() (policyGroupsPayload, error) {
	var out policyGroupsPayload
	err := c.getJSON("/api/v1/policy-groups", &out)
	return out, err
}

func (c apiClient) traffic() (trafficSnapshotPayload, error) {
	var out trafficSnapshotPayload
	err := c.getJSON("/api/v1/traffic?limit=200", &out)
	return out, err
}

// trafficWithFilters fetches traffic with optional token-based filter params.
func (c apiClient) trafficWithFilters(action, app, domain, country, port, query string) (trafficSnapshotPayload, error) {
	path := "/api/v1/traffic?limit=200"
	if action != "" {
		path += "&action=" + action
	}
	if app != "" {
		path += "&app=" + app
	}
	if domain != "" {
		path += "&domain=" + domain
	}
	if country != "" {
		path += "&country=" + country
	}
	if port != "" {
		path += "&port=" + port
	}
	if query != "" {
		path += "&query=" + query
	}
	var out trafficSnapshotPayload
	err := c.getJSON(path, &out)
	return out, err
}

func (c apiClient) developer() (developerStatusPayload, []developerEntryPayload, error) {
	status, err := c.developerStatus()
	if err != nil {
		return developerStatusPayload{}, nil, err
	}
	entries, err := c.developerEntries()
	return status, entries, err
}

func (c apiClient) developerStatus() (developerStatusPayload, error) {
	var out developerStatusPayload
	err := c.getJSON("/api/v1/developer/status", &out)
	return out, err
}

func (c apiClient) developerEntries() ([]developerEntryPayload, error) {
	var out developerEntriesPayload
	err := c.getJSON("/api/v1/developer/entries?limit=200", &out)
	return out.Entries, err
}

func (c apiClient) clearDeveloperEntries() error {
	return c.doNoBody(http.MethodDelete, "/api/v1/developer/entries", nil)
}

func (c apiClient) exportDeveloperHAR(path string) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v1/developer/har", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (c apiClient) connect() error {
	return c.doNoBody(http.MethodPost, "/api/v1/connect", nil)
}

func (c apiClient) disconnect() error {
	return c.doNoBody(http.MethodPost, "/api/v1/disconnect", nil)
}

func (c apiClient) setActiveProfile(name string) error {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	return c.doNoBody(http.MethodPut, "/api/v1/profiles/active", bytes.NewReader(body))
}

func (c apiClient) testPolicyGroup(group string) (policyGroupsPayload, error) {
	body, err := json.Marshal(map[string]string{"group": group})
	if err != nil {
		return policyGroupsPayload{}, err
	}
	var out policyGroupsPayload
	err = c.doJSON(http.MethodPost, "/api/v1/policy-groups/test", bytes.NewReader(body), &out)
	return out, err
}

func (c apiClient) selectPolicyGroup(profile, group, chain string) (policyGroupsPayload, error) {
	body, err := json.Marshal(map[string]string{"profile": profile, "group": group, "chain": chain})
	if err != nil {
		return policyGroupsPayload{}, err
	}
	var out policyGroupSelectionPayload
	if err := c.doJSON(http.MethodPut, "/api/v1/policy-groups/selection", bytes.NewReader(body), &out); err != nil {
		return policyGroupsPayload{}, err
	}
	if len(out.PolicyGroups.Groups) > 0 || out.PolicyGroups.Profile != "" {
		return out.PolicyGroups, nil
	}
	return policyGroupsPayload{Profile: out.Profile, Groups: out.Groups}, nil
}

func (c apiClient) createRule(rule rulePayload) error {
	body, err := json.Marshal(createRuleRequest{
		Rule:     rule,
		Position: "append",
	})
	if err != nil {
		return err
	}
	return c.doNoBody(http.MethodPost, "/api/v1/rules", bytes.NewReader(body))
}

func (c apiClient) createRuleFromConnection(req createRuleFromConnectionRequest) error {
	req.Position = "append"
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.doNoBody(http.MethodPost, "/api/v1/rules/from-connection", bytes.NewReader(body))
}

func (c apiClient) createTemporaryRuleFromConnection(connID, profile, name, action, scope string, ttlSeconds int64) error {
	body, err := json.Marshal(map[string]any{
		"conn_id":     connID,
		"profile":     profile,
		"name":        name,
		"action":      action,
		"scope":       scope,
		"ttl_seconds": ttlSeconds,
	})
	if err != nil {
		return err
	}
	return c.doNoBody(http.MethodPost, "/api/v1/rules/temporary/from-connection", bytes.NewReader(body))
}

func (c apiClient) cleanupRule(req cleanupRuleRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.doNoBody(http.MethodPost, "/api/v1/rules/cleanup", bytes.NewReader(body))
}

func (c apiClient) testRule(network, target string) (ruleTestResponse, error) {
	body, err := json.Marshal(ruleTestRequest{Network: network, Target: target})
	if err != nil {
		return ruleTestResponse{}, err
	}
	var out ruleTestResponse
	err = c.doJSON(http.MethodPost, "/api/v1/rules/test", bytes.NewReader(body), &out)
	return out, err
}

func (c apiClient) eventsURL() string {
	return c.wsBaseURL + "/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
}

func (c apiClient) getJSON(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c apiClient) doNoBody(method, path string, body io.Reader) error {
	return c.doJSON(method, path, body, nil)
}

func (c apiClient) doJSON(method, path string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = resp.Status
	}
	return fmt.Errorf("%s: %s", resp.Status, text)
}

// --- rule subscriptions ---

type ruleSubscriptionsPayload struct {
	Profile       string                    `json:"profile"`
	Subscriptions []ruleSubscriptionPayload `json:"subscriptions"`
}

type ruleSubscriptionPayload struct {
	Name     string                        `json:"name"`
	URL      string                        `json:"url"`
	Format   string                        `json:"format"`
	Action   string                        `json:"action"`
	Disabled bool                          `json:"disabled"`
	Status   ruleSubscriptionStatusPayload `json:"status"`
}

type ruleSubscriptionStatusPayload struct {
	EntryCount   int    `json:"entry_count"`
	LastFetchErr string `json:"last_fetch_error,omitempty"`
	FetchedAt    int64  `json:"fetched_at_ts_ns,omitempty"`
}

func (c apiClient) ruleSubscriptions(profile string) (ruleSubscriptionsPayload, error) {
	path := "/api/v1/rule-subscriptions"
	if profile != "" {
		path += "?profile=" + profile
	}
	var out ruleSubscriptionsPayload
	err := c.getJSON(path, &out)
	return out, err
}

func (c apiClient) refreshRuleSubscriptions(profile string, names []string) error {
	body, err := json.Marshal(map[string]any{"profile": profile, "names": names})
	if err != nil {
		return err
	}
	return c.doNoBody(http.MethodPost, "/api/v1/rule-subscriptions/refresh", bytes.NewReader(body))
}

// --- config import/export ---

type configImportResponse struct {
	Profiles   []string `json:"profiles"`
	Active     string   `json:"active"`
	BackupPath string   `json:"backup_path"`
	Message    string   `json:"message"`
}

func (c apiClient) exportConfig() (string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v1/config/export", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", responseError(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c apiClient) importConfig(toml string) (configImportResponse, error) {
	var out configImportResponse
	err := c.doJSON(http.MethodPost, "/api/v1/config/import", strings.NewReader(toml), &out)
	return out, err
}
