package developer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	req, err := http.NewRequestWithContext(ctx, method, rawURL, strings.NewReader(bodyText))
	if err != nil {
		return RepeatResponse{}, err
	}
	if err := subscription.ValidatePublicHTTPURL(ctx, req.URL); err != nil {
		return RepeatResponse{}, err
	}
	if len(repeat.Headers) > 0 {
		for _, header := range repeat.Headers {
			req.Header.Add(header.Name, header.Value)
		}
	} else {
		for _, header := range entry.Request.Headers {
			if header.Redacted {
				continue
			}
			req.Header.Add(header.Name, header.Value)
		}
	}

	m.mu.RLock()
	cfg := m.cfg
	store := m.store
	m.mu.RUnlock()
	if !cfg.Enabled || store == nil {
		return RepeatResponse{}, errors.New("developer capture disabled")
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
