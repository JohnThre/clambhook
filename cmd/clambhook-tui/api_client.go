package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type apiClient struct {
	baseURL    string
	wsBaseURL  string
	httpClient *http.Client
}

type statusPayload struct {
	Running   bool                    `json:"running"`
	Profile   string                  `json:"profile"`
	Listeners []listenerStatusPayload `json:"listeners,omitempty"`
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

type chainPayload struct {
	Name    string          `json:"name"`
	Servers []serverPayload `json:"servers"`
}

type serverPayload struct {
	Name     string          `json:"name"`
	Address  string          `json:"address"`
	Protocol string          `json:"protocol"`
	Geo      locationPayload `json:"geo"`
	GeoError string          `json:"geo_error,omitempty"`
}

type locationPayload struct {
	Country     string  `json:"country,omitempty"`
	CountryCode string  `json:"country_code,omitempty"`
	City        string  `json:"city,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type trafficSnapshotPayload struct {
	UpdatedTsNs int64                      `json:"updated_ts_ns"`
	Summary     trafficSummaryPayload      `json:"summary"`
	Connections []trafficConnectionPayload `json:"connections"`
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
	ConnID      string          `json:"conn_id"`
	State       string          `json:"state"`
	StartTsNs   int64           `json:"start_ts_ns"`
	UpdatedTsNs int64           `json:"updated_ts_ns"`
	EndTsNs     int64           `json:"end_ts_ns,omitempty"`
	Listener    listenerInfo    `json:"listener"`
	ClientAddr  string          `json:"client_addr,omitempty"`
	ChainName   string          `json:"chain_name,omitempty"`
	Target      string          `json:"target,omitempty"`
	TargetHost  string          `json:"target_host,omitempty"`
	TargetPort  string          `json:"target_port,omitempty"`
	Network     string          `json:"network,omitempty"`
	Application string          `json:"application,omitempty"`
	Hops        []trafficHop    `json:"hops,omitempty"`
	Geo         locationPayload `json:"geo"`
	GeoError    string          `json:"geo_error,omitempty"`
	TotalDialNs int64           `json:"total_dial_ns,omitempty"`
	RxBps       float64         `json:"rx_bps"`
	TxBps       float64         `json:"tx_bps"`
	RxTotal     uint64          `json:"rx_total"`
	TxTotal     uint64          `json:"tx_total"`
	DurationNs  int64           `json:"duration_ns,omitempty"`
	CloseReason string          `json:"close_reason,omitempty"`
}

type listenerInfo struct {
	Protocol string `json:"protocol"`
	Addr     string `json:"addr"`
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

func (c apiClient) traffic() (trafficSnapshotPayload, error) {
	var out trafficSnapshotPayload
	err := c.getJSON("/api/v1/traffic?limit=200", &out)
	return out, err
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

func (c apiClient) eventsURL() string {
	return c.wsBaseURL + "/api/v1/events?types=connection.*,log.*"
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
