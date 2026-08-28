package developer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
	"github.com/JohnThre/clambhook/internal/subscription"
)

const breakpointTimeout = 30 * time.Second

// repeatHTTPClient is the HTTP client used by Repeat. It is overridable in
// tests so replay traffic can be routed to a local server without touching
// the public network.
var repeatHTTPClient *http.Client

// RepeatRequest asks the daemon to resend a captured request.
type RepeatRequest struct {
	EntryID string   `json:"entry_id"`
	Method  string   `json:"method,omitempty"`
	URL     string   `json:"url,omitempty"`
	Headers []Header `json:"headers,omitempty"`
	Body    *string  `json:"body,omitempty"`
}

// RepeatResponse contains the captured repeat result.
type RepeatResponse struct {
	Entry Entry `json:"entry"`
}

// BreakpointMessage is an editable request or response snapshot.
type BreakpointMessage struct {
	Method  string   `json:"method,omitempty"`
	URL     string   `json:"url,omitempty"`
	Status  int      `json:"status,omitempty"`
	Headers []Header `json:"headers,omitempty"`
	Body    string   `json:"body,omitempty"`
	BodySet bool     `json:"body_set,omitempty"`
}

// PendingBreakpoint is a paused request or response awaiting user action.
type PendingBreakpoint struct {
	ID        string             `json:"id"`
	RuleID    string             `json:"rule_id"`
	RuleName  string             `json:"rule_name,omitempty"`
	Stage     string             `json:"stage"`
	CreatedAt time.Time          `json:"created_at"`
	Request   BreakpointMessage  `json:"request"`
	Response  *BreakpointMessage `json:"response,omitempty"`
}

// BreakpointResolution resumes or drops a pending breakpoint.
type BreakpointResolution struct {
	Action   string             `json:"action"`
	Request  *BreakpointMessage `json:"request,omitempty"`
	Response *BreakpointMessage `json:"response,omitempty"`
}

type pendingBreakpoint struct {
	PendingBreakpoint
	ch chan BreakpointResolution
}

// MapRequest evaluates map rules for req. The returned request is either req or
// a shallow clone with a rewritten URL.
func (m *Manager) MapRequest(req *http.Request) (*http.Request, *listener.HTTPMapResult, error) {
	if m == nil || req == nil {
		return req, nil, nil
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return req, nil, nil
	}
	for _, rule := range cfg.MapRules {
		if !rule.Enabled || !matchRequest(rule.Match, req) {
			continue
		}
		switch rule.Kind {
		case "local":
			resp, err := localMapResponse(rule, req)
			if err != nil {
				return req, nil, err
			}
			return req, &listener.HTTPMapResult{
				RuleID:   rule.ID,
				RuleName: rule.Name,
				Kind:     rule.Kind,
				Local:    resp,
			}, nil
		case "remote":
			rewritten, err := rewriteRequestURL(req, rule)
			if err != nil {
				return req, nil, err
			}
			return rewritten, &listener.HTTPMapResult{
				RuleID:    rule.ID,
				RuleName:  rule.Name,
				Kind:      rule.Kind,
				RemoteURL: rewritten.URL.String(),
			}, nil
		}
	}
	return req, nil, nil
}

// PendingBreakpoints returns pending breakpoints newest first.
func (m *Manager) PendingBreakpoints() []PendingBreakpoint {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PendingBreakpoint, 0, len(m.pending))
	for _, pending := range m.pending {
		out = append(out, pending.PendingBreakpoint)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// ResolveBreakpoint resumes a paused breakpoint.
func (m *Manager) ResolveBreakpoint(id string, resolution BreakpointResolution) bool {
	if m == nil {
		return false
	}
	id = strings.TrimSpace(id)
	m.mu.Lock()
	pending := m.pending[id]
	if pending != nil {
		delete(m.pending, id)
	}
	m.mu.Unlock()
	if pending == nil {
		return false
	}
	if strings.TrimSpace(resolution.Action) == "" {
		resolution.Action = "continue"
	}
	select {
	case pending.ch <- resolution:
	default:
	}
	return true
}

// BreakpointRequest pauses a matching request and returns the chosen action.
func (m *Manager) BreakpointRequest(ctx context.Context, req *http.Request, body []byte) (listener.HTTPBreakpointResolution, bool, error) {
	return m.breakpoint(ctx, "request", req, nil, body)
}

// HasRequestBreakpoint reports whether req matches a request breakpoint.
func (m *Manager) HasRequestBreakpoint(req *http.Request) bool {
	return m.hasBreakpoint("request", req)
}

// HasResponseBreakpoint reports whether req matches a response breakpoint.
func (m *Manager) HasResponseBreakpoint(req *http.Request) bool {
	return m.hasBreakpoint("response", req)
}

// BreakpointResponse pauses a matching response and returns the chosen action.
func (m *Manager) BreakpointResponse(ctx context.Context, req *http.Request, resp *http.Response, body []byte) (listener.HTTPBreakpointResolution, bool, error) {
	return m.breakpoint(ctx, "response", req, resp, body)
}

func (m *Manager) hasBreakpoint(stage string, req *http.Request) bool {
	if m == nil || req == nil {
		return false
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return false
	}
	for _, rule := range cfg.BreakpointRules {
		if rule.Enabled && breakpointStageMatches(rule.Stage, stage) && matchRequest(rule.Match, req) {
			return true
		}
	}
	return false
}

// HasRequestRewrite reports whether req matches an enabled request-stage
// rewrite rule. The listener uses it to decide whether to drain the request
// body before applying rewrites.
func (m *Manager) HasRequestRewrite(req *http.Request) bool {
	return m.hasRewrite("request", req)
}

// HasResponseRewrite reports whether req matches an enabled response-stage
// rewrite rule.
func (m *Manager) HasResponseRewrite(req *http.Request) bool {
	return m.hasRewrite("response", req)
}

func (m *Manager) hasRewrite(stage string, req *http.Request) bool {
	if m == nil || req == nil {
		return false
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return false
	}
	for _, rule := range cfg.RewriteRules {
		if rule.Enabled && breakpointStageMatches(rule.Stage, stage) && matchRequest(rule.Match, req) {
			return true
		}
	}
	return false
}

// RewriteRequest applies matching request-stage rewrite ops to req. Header ops
// mutate req.Header in place; body ops return the new body and bodySet so the
// caller can reset req.Body and Content-Length. stage "response" rules are
// skipped here.
func (m *Manager) RewriteRequest(req *http.Request, body []byte) (newBody []byte, bodySet bool, err error) {
	if m == nil || req == nil {
		return nil, false, nil
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return nil, false, nil
	}
	for _, rule := range cfg.RewriteRules {
		if !rule.Enabled || !breakpointStageMatches(rule.Stage, "request") || !matchRequest(rule.Match, req) {
			continue
		}
		for _, op := range rule.Ops {
			switch op.Target {
			case "header":
				applyHeaderRewrite(req.Header, op)
			case "body":
				if nb, set := applyBodyRewrite(body, op); set {
					body = nb
					bodySet = true
				}
			}
		}
	}
	if bodySet {
		return body, true, nil
	}
	return nil, false, nil
}

// RewriteResponse applies matching response-stage rewrite ops to resp. Status
// and header ops mutate resp in place; body ops return the new body and bodySet.
func (m *Manager) RewriteResponse(req *http.Request, resp *http.Response, body []byte) (newBody []byte, bodySet bool, err error) {
	if m == nil || req == nil || resp == nil {
		return nil, false, nil
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return nil, false, nil
	}
	for _, rule := range cfg.RewriteRules {
		if !rule.Enabled || !breakpointStageMatches(rule.Stage, "response") || !matchRequest(rule.Match, req) {
			continue
		}
		for _, op := range rule.Ops {
			switch op.Target {
			case "status":
				if code, parseErr := strconv.Atoi(strings.TrimSpace(op.Value)); parseErr == nil && code >= 100 && code <= 599 {
					resp.StatusCode = code
					resp.Status = fmt.Sprintf("%d %s", code, http.StatusText(code))
				}
			case "header":
				applyHeaderRewrite(resp.Header, op)
			case "body":
				if nb, set := applyBodyRewrite(body, op); set {
					body = nb
					bodySet = true
				}
			}
		}
	}
	if bodySet {
		return body, true, nil
	}
	return nil, false, nil
}

// applyHeaderRewrite mutates hdr for one header op.
func applyHeaderRewrite(hdr http.Header, op config.DeveloperRewriteOp) {
	switch op.Action {
	case "add":
		hdr.Add(op.Field, op.Value)
	case "set":
		hdr.Set(op.Field, op.Value)
	case "remove":
		hdr.Del(op.Field)
	}
}

// applyBodyRewrite returns the rewritten body and whether it changed.
func applyBodyRewrite(body []byte, op config.DeveloperRewriteOp) ([]byte, bool) {
	switch op.Action {
	case "set":
		return []byte(op.Value), true
	case "replace":
		if len(op.Value) == 0 {
			return body, false
		}
		return bytes.ReplaceAll(body, []byte(op.Value), []byte(op.Replace)), true
	}
	return body, false
}

func (m *Manager) breakpoint(ctx context.Context, stage string, req *http.Request, resp *http.Response, body []byte) (listener.HTTPBreakpointResolution, bool, error) {
	if m == nil || req == nil {
		return listener.HTTPBreakpointResolution{}, false, nil
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return listener.HTTPBreakpointResolution{}, false, nil
	}
	for _, rule := range cfg.BreakpointRules {
		if !rule.Enabled || !breakpointStageMatches(rule.Stage, stage) || !matchRequest(rule.Match, req) {
			continue
		}
		pending := &pendingBreakpoint{
			PendingBreakpoint: PendingBreakpoint{
				ID:        fmt.Sprintf("bp-%d", m.nextPending.Add(1)),
				RuleID:    rule.ID,
				RuleName:  rule.Name,
				Stage:     stage,
				CreatedAt: time.Now(),
				Request: BreakpointMessage{
					Method:  req.Method,
					URL:     requestURLForBreakpoint(req),
					Headers: cloneHeaders(req.Header, cfg),
					Body:    string(body),
					BodySet: body != nil,
				},
			},
			ch: make(chan BreakpointResolution, 1),
		}
		if resp != nil {
			pending.Response = &BreakpointMessage{
				Status:  resp.StatusCode,
				Headers: cloneHeaders(resp.Header, cfg),
				Body:    string(body),
				BodySet: body != nil,
			}
		}
		m.mu.Lock()
		if m.pending == nil {
			m.pending = make(map[string]*pendingBreakpoint)
		}
		m.pending[pending.ID] = pending
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			delete(m.pending, pending.ID)
			m.mu.Unlock()
		}()

		waitCtx, cancel := context.WithTimeout(ctx, breakpointTimeout)
		defer cancel()
		select {
		case resolution := <-pending.ch:
			if strings.TrimSpace(resolution.Action) == "" {
				resolution.Action = "continue"
			}
			return toHTTPBreakpointResolution(resolution), true, nil
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return listener.HTTPBreakpointResolution{Action: "continue"}, true, nil
			}
			return listener.HTTPBreakpointResolution{}, true, waitCtx.Err()
		}
	}
	return listener.HTTPBreakpointResolution{}, false, nil
}

// Repeat resends a captured request directly from the daemon.
// ComposedRequest is a standalone request to send through the daemon capture
// pipeline, independent of any existing capture. It backs the
// /developer/send endpoint used by the compose window and cURL import.
type ComposedRequest struct {
	Method  string   `json:"method,omitempty"`
	URL     string   `json:"url,omitempty"`
	Headers []Header `json:"headers,omitempty"`
	Body    *string  `json:"body,omitempty"`
}

// Send executes a composed request directly (no source capture), captures the
// result into the store, and returns it. It is the standalone counterpart to
// Repeat, used by the compose window and cURL import.
func (m *Manager) Send(ctx context.Context, composed ComposedRequest) (RepeatResponse, error) {
	if m == nil {
		return RepeatResponse{}, errors.New("developer mode disabled")
	}
	method := strings.TrimSpace(composed.Method)
	if method == "" {
		method = "GET"
	}
	rawURL := strings.TrimSpace(composed.URL)
	bodyText := ""
	if composed.Body != nil {
		bodyText = *composed.Body
	}
	return m.sendAndCapture(ctx, method, rawURL, composed.Headers, bodyText)
}

// Repeat resends a captured request, applying any per-field overrides on top
// of the stored capture. It requires an existing entry id.
func (m *Manager) Repeat(ctx context.Context, repeat RepeatRequest) (RepeatResponse, error) {
	if m == nil {
		return RepeatResponse{}, errors.New("developer mode disabled")
	}
	entry, ok := m.Get(strings.TrimSpace(repeat.EntryID))
	if !ok {
		return RepeatResponse{}, errors.New("developer entry not found")
	}
	method := strings.TrimSpace(repeat.Method)
	if method == "" {
		method = entry.Method
	}
	rawURL := strings.TrimSpace(repeat.URL)
	if rawURL == "" {
		rawURL = entry.URL
	}
	bodyText := entry.Request.Body.Preview
	if repeat.Body != nil {
		bodyText = *repeat.Body
	} else if entry.Request.Body.Truncated {
		return RepeatResponse{}, errors.New("captured request body is truncated; provide an override body")
	}
	headers := repeat.Headers
	if len(headers) == 0 {
		headers = make([]Header, 0, len(entry.Request.Headers))
		for _, h := range entry.Request.Headers {
			if h.Redacted {
				continue
			}
			headers = append(headers, h)
		}
	}
	return m.sendAndCapture(ctx, method, rawURL, headers, bodyText)
}

// sendAndCapture builds and sends an HTTP request through the developer
// capture pipeline, recording the transaction in the store. It is shared by
// Repeat (resolved from a capture) and Send (standalone compose). The sent
// request body gets a viewer so a composed JSON body renders pretty in the
// capture; the response viewer is set by the body-capture snapshot.
func (m *Manager) sendAndCapture(ctx context.Context, method, rawURL string, headers []Header, bodyText string) (RepeatResponse, error) {
	m.mu.RLock()
	cfg := m.cfg
	store := m.store
	m.mu.RUnlock()
	if !cfg.Enabled || store == nil {
		return RepeatResponse{}, errors.New("developer capture disabled")
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, strings.NewReader(bodyText))
	if err != nil {
		return RepeatResponse{}, err
	}
	if err := subscription.ValidatePublicHTTPURL(ctx, req.URL); err != nil {
		return RepeatResponse{}, err
	}
	for _, header := range headers {
		req.Header.Add(header.Name, header.Value)
	}

	started := time.Now()
	out := Entry{
		ID:        fmt.Sprintf("dev-%d", m.nextID.Add(1)),
		ChainName: "repeat",
		StartedAt: started,
		Method:    req.Method,
		URL:       redactCapturedURL(req.URL.String(), cfg),
		Scheme:    req.URL.Scheme,
		Host:      req.URL.Host,
		Request: Message{
			Headers: cloneHeaders(req.Header, cfg),
			Cookies: cloneRequestCookies(req, cfg),
			Body: Body{
				Size:           int64(len(bodyText)),
				Preview:        bodyText,
				PreviewBytes:   int64(len(bodyText)),
				Truncated:      false,
				TruncatedAfter: cfg.BodyLimitBytes,
				MimeType:       req.Header.Get("Content-Type"),
				Encoding:       "utf8",
			},
		},
	}
	out.Request.Body.Viewer = computeViewer(out.Request.Body.MimeType, []byte(bodyText), true, false)
	client := repeatHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := subscription.ClientWithSafeRedirects(client).Do(req)
	out.FinishedAt = time.Now()
	if err != nil {
		out.Error = err.Error()
		store.Add(out)
		return RepeatResponse{Entry: out}, nil
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.Response.Headers = cloneHeaders(resp.Header, cfg)
	out.Response.Cookies = cloneResponseCookies(resp, cfg)
	cap := newBodyCapture(cfg.BodyLimitBytes)
	_, copyErr := io.Copy(cap, resp.Body)
	out.Response.Body = cap.snapshot(out.Response.Headers)
	if copyErr != nil {
		out.Error = copyErr.Error()
	}
	store.Add(out)
	return RepeatResponse{Entry: out}, nil
}

func (b *bodyCapture) Write(p []byte) (int, error) {
	b.write(p)
	return len(p), nil
}

func matchRequest(match config.DeveloperMatchConfig, req *http.Request) bool {
	if len(match.Methods) > 0 {
		ok := false
		for _, method := range match.Methods {
			if strings.EqualFold(method, req.Method) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	host := requestHostForMatch(req)
	if match.Host != "" && !strings.EqualFold(match.Host, host) {
		return false
	}
	path := "/"
	if req.URL != nil && req.URL.RequestURI() != "" {
		path = req.URL.RequestURI()
	}
	if match.PathPrefix != "" && !strings.HasPrefix(path, match.PathPrefix) {
		return false
	}
	if match.URLContains != "" && !strings.Contains(requestURLForBreakpoint(req), match.URLContains) {
		return false
	}
	return true
}

func breakpointStageMatches(ruleStage, stage string) bool {
	return ruleStage == "both" || ruleStage == stage
}

func requestHostForMatch(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}
	if req.URL != nil {
		return req.URL.Host
	}
	return ""
}

func requestURLForBreakpoint(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	if req.URL.IsAbs() {
		return req.URL.String()
	}
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	host := requestHostForMatch(req)
	if host == "" {
		return req.URL.RequestURI()
	}
	return scheme + "://" + host + req.URL.RequestURI()
}

func localMapResponse(rule config.DeveloperMapRuleConfig, req *http.Request) (*listener.HTTPLocalMapResponse, error) {
	path := rule.LocalPath
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		reqPath := "/"
		if req.URL != nil && req.URL.Path != "" {
			reqPath = req.URL.Path
		}
		clean := filepath.Clean("/" + strings.TrimPrefix(reqPath, "/"))
		path = filepath.Join(rule.LocalPath, strings.TrimPrefix(clean, "/"))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	status := rule.Status
	if status == 0 {
		status = http.StatusOK
	}
	header := make(http.Header)
	for name, value := range rule.Headers {
		header.Set(name, value)
	}
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", http.DetectContentType(body))
	}
	return &listener.HTTPLocalMapResponse{Status: status, Header: header, Body: body}, nil
}

func rewriteRequestURL(req *http.Request, rule config.DeveloperMapRuleConfig) (*http.Request, error) {
	base, err := url.Parse(rule.RemoteURL)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	rewritten := *req.URL
	rewritten.Scheme = base.Scheme
	rewritten.Host = base.Host
	if base.Path != "" && base.Path != "/" {
		suffix := rewritten.Path
		if rule.Match.PathPrefix != "" {
			suffix = strings.TrimPrefix(suffix, rule.Match.PathPrefix)
		}
		rewritten.Path = joinURLPath(base.Path, suffix)
	}
	if base.RawQuery != "" {
		rewritten.RawQuery = base.RawQuery
	}
	clone.URL = &rewritten
	clone.Host = rewritten.Host
	return clone, nil
}

func joinURLPath(basePath, suffix string) string {
	if suffix == "" || suffix == "/" {
		return basePath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func toHTTPBreakpointResolution(resolution BreakpointResolution) listener.HTTPBreakpointResolution {
	out := listener.HTTPBreakpointResolution{Action: resolution.Action}
	if resolution.Request != nil {
		out.Request = toHTTPBreakpointMessage(*resolution.Request)
	}
	if resolution.Response != nil {
		out.Response = toHTTPBreakpointMessage(*resolution.Response)
	}
	return out
}

func toHTTPBreakpointMessage(message BreakpointMessage) *listener.HTTPBreakpointMessage {
	headers := make([]listener.HTTPHeader, 0, len(message.Headers))
	for _, header := range message.Headers {
		if header.Name == "" {
			continue
		}
		headers = append(headers, listener.HTTPHeader{Name: header.Name, Value: header.Value})
	}
	return &listener.HTTPBreakpointMessage{
		Method:  message.Method,
		URL:     message.URL,
		Status:  message.Status,
		Headers: headers,
		Body:    message.Body,
		BodySet: message.BodySet,
	}
}
