package scripting

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// defaultMaxExecutionTime is the default wall-clock budget for one hook
// invocation. Misbehaving scripts are terminated when it expires.
const defaultMaxExecutionTime = 5 * time.Second

// maxScriptBodyBytes caps how much of a request body is exposed to scripts.
const maxScriptBodyBytes = 64 << 10 // 64 KiB

// compiledScript is the parsed, runtime-independent form of a module script.
type compiledScript struct {
	program       *goja.Program
	source        string
	hasOnRequest  bool
	hasOnResponse bool
	hasOnCron     bool
	err           error
}

// compileScript parses a script and records which global hook functions it
// defines. A script that fails to parse is still returned so callers can
// surface the error without crashing the daemon.
func compileScript(src string) *compiledScript {
	cs := &compiledScript{source: src}
	program, err := goja.Compile("__module__", src, false)
	if err != nil {
		cs.err = fmt.Errorf("compile script: %w", err)
		return cs
	}
	cs.program = program
	// Detect exported hook functions by scanning the source text. This avoids
	// needing to execute the script at compile time, which keeps module load
	// side-effect-free.
	cs.hasOnRequest = hasFunction(src, "onRequest")
	cs.hasOnResponse = hasFunction(src, "onResponse")
	cs.hasOnCron = hasFunction(src, "onCron")
	return cs
}

// hasFunction is a cheap source-level heuristic for common function
// declaration patterns.
func hasFunction(src, name string) bool {
	patterns := []string{
		"function " + name,
		"var " + name,
		"const " + name,
		"let " + name,
		name + " =",
		name + "=",
	}
	for _, p := range patterns {
		if strings.Contains(src, p) {
			return true
		}
	}
	return false
}

// requestResult carries the outcome of an onRequest hook.
type requestResult struct {
	req      *http.Request
	modified bool
	err      error
}

// RunRequestHook executes enabled modules' onRequest hooks sequentially. Each
// hook receives the current request and may mutate headers or the URL. The
// returned request reflects the final accumulated mutation.
func (m *Manager) RunRequestHook(req *http.Request) (*http.Request, error) {
	m.mu.RLock()
	modules := m.modules
	m.mu.RUnlock()

	current := req
	for _, cm := range modules {
		if !cm.cfg.Enabled || cm.program == nil || !cm.program.hasOnRequest {
			continue
		}
		next, err := m.runModuleRequestHook(cm, current)
		if err != nil {
			m.log(cm.cfg.Name, fmt.Sprintf("request hook error: %v", err))
			continue
		}
		if next != nil {
			current = next
		}
	}
	return current, nil
}

func (m *Manager) runModuleRequestHook(cm compiledModule, req *http.Request) (*http.Request, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultMaxExecutionTime)
	defer cancel()

	rt := goja.New()
	if err := m.bindAPI(rt, cm.cfg.Name, cm.cfg.AllowNetwork); err != nil {
		return nil, err
	}

	body, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	reqObj := map[string]any{
		"method":  req.Method,
		"url":     req.URL.String(),
		"scheme":  req.URL.Scheme,
		"host":    req.Host,
		"path":    req.URL.Path,
		"headers": headerMap(req.Header),
		"body":    string(body),
	}
	if err := rt.Set("$request", reqObj); err != nil {
		return nil, err
	}

	doneCh := make(chan requestResult, 1)
	doneFn := func(call goja.FunctionCall) goja.Value {
		arg := call.Argument(0)
		if goja.IsUndefined(arg) || goja.IsNull(arg) {
			doneCh <- requestResult{req: req, modified: false}
			return goja.Undefined()
		}
		obj := arg.ToObject(rt)
		next, modified, err := applyRequestResult(rt, obj, req, body)
		doneCh <- requestResult{req: next, modified: modified, err: err}
		return goja.Undefined()
	}
	if err := rt.Set("$done", doneFn); err != nil {
		return nil, err
	}

	if _, err := rt.RunProgram(cm.program.program); err != nil {
		return nil, fmt.Errorf("run module: %w", err)
	}

	// If the script defines onRequest, call it.
	var onRequest goja.Value
	if v := rt.Get("onRequest"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		onRequest = v
	}
	if onRequest == nil {
		doneCh <- requestResult{req: req, modified: false}
	}

	if onRequest != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					doneCh <- requestResult{err: fmt.Errorf("panic: %v", r)}
				}
			}()
			callable, ok := goja.AssertFunction(rt.ToValue(onRequest))
			if !ok {
				doneCh <- requestResult{err: fmt.Errorf("onRequest is not callable")}
				return
			}
			if _, err := callable(goja.Undefined(), rt.ToValue(reqObj)); err != nil {
				doneCh <- requestResult{err: err}
			}
		}()
	}

	select {
	case res := <-doneCh:
		return res.req, res.err
	case <-ctx.Done():
		return nil, fmt.Errorf("script timed out after %v", defaultMaxExecutionTime)
	}
}

// applyRequestResult converts the object passed to $done back into an
// *http.Request. Only safe mutations are applied: method, url, headers, and a
// new body string.
func applyRequestResult(rt *goja.Runtime, obj *goja.Object, original *http.Request, originalBody []byte) (*http.Request, bool, error) {
	if obj == nil {
		return original, false, nil
	}
	modified := false
	next := original.Clone(original.Context())

	if v := obj.Get("method"); v != nil && !goja.IsUndefined(v) {
		next.Method = v.String()
		modified = true
	}
	if v := obj.Get("url"); v != nil && !goja.IsUndefined(v) {
		u, err := parseURL(v.String())
		if err != nil {
			return nil, false, fmt.Errorf("invalid url: %w", err)
		}
		next.URL = u
		next.Host = u.Host
		modified = true
	}
	if v := obj.Get("headers"); v != nil && !goja.IsUndefined(v) {
		hmap := v.ToObject(rt)
		newHdr := make(http.Header)
		for _, key := range hmap.Keys() {
			val := hmap.Get(key)
			if val == nil || goja.IsUndefined(val) {
				continue
			}
			if goja.IsNull(val) {
				continue
			}
			str := val.String()
			newHdr.Set(key, str)
			modified = true
		}
		if len(newHdr) > 0 {
			next.Header = newHdr
		}
	}
	if v := obj.Get("body"); v != nil && !goja.IsUndefined(v) {
		bodyStr := v.String()
		next.Body = io.NopCloser(bytes.NewReader([]byte(bodyStr)))
		next.ContentLength = int64(len(bodyStr))
		modified = true
	} else if len(originalBody) > 0 {
		// Restore the original body so the request remains readable.
		next.Body = io.NopCloser(bytes.NewReader(originalBody))
		next.ContentLength = int64(len(originalBody))
	}
	return next, modified, nil
}

func parseURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

func readRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxScriptBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxScriptBodyBytes {
		return nil, fmt.Errorf("request body exceeds %d bytes", maxScriptBodyBytes)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func headerMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}
