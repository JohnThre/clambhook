package scripting

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

func TestManagerRunsRequestHookAndMutatesHeaders(t *testing.T) {
	modules := []config.ModuleConfig{{
		Name:    "strip-auth",
		Enabled: true,
		Script: `
function onRequest(req) {
	delete req.headers["Authorization"];
	req.headers["X-Scripted"] = "yes";
	$done(req);
}
`,
	}}
	mgr, err := NewManager(modules, t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	if !mgr.Enabled() {
		t.Fatal("manager should report enabled")
	}

	req := httptest.NewRequest("GET", "http://example.com/path", nil)
	req.Header.Set("Authorization", "Bearer secret")

	got, err := mgr.RunRequestHook(req)
	if err != nil {
		t.Fatalf("run hook: %v", err)
	}
	if got.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization header not removed: %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get("X-Scripted") != "yes" {
		t.Fatalf("script header not added: %q", got.Header.Get("X-Scripted"))
	}
}

func TestManagerRestoresRequestBody(t *testing.T) {
	modules := []config.ModuleConfig{{
		Name:    "no-op",
		Enabled: true,
		Script:  "function onRequest(req) { $done(req); }",
	}}
	mgr, err := NewManager(modules, t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	req := httptest.NewRequest("POST", "http://example.com/path", strings.NewReader("hello"))
	got, err := mgr.RunRequestHook(req)
	if err != nil {
		t.Fatalf("run hook: %v", err)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != "hello" {
		t.Fatalf("body not preserved: %q", string(body))
	}
}

func TestDisabledModuleNotInvoked(t *testing.T) {
	modules := []config.ModuleConfig{{
		Name:    "disabled",
		Enabled: false,
		Script:  "function onRequest(req) { req.headers['X-Bad'] = 'bad'; $done(req); }",
	}}
	mgr, err := NewManager(modules, t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	if mgr.Enabled() {
		t.Fatal("manager should report disabled when all modules disabled")
	}

	req := httptest.NewRequest("GET", "http://example.com/path", nil)
	got, err := mgr.RunRequestHook(req)
	if err != nil {
		t.Fatalf("run hook: %v", err)
	}
	if got.Header.Get("X-Bad") != "" {
		t.Fatal("disabled module ran")
	}
}

func TestPersistentStoreRoundTrip(t *testing.T) {
	modules := []config.ModuleConfig{{
		Name:    "store-test",
		Enabled: true,
		Script: `
function onRequest(req) {
	clambhook.persistentStore.write("seen", req.path);
	var seen = clambhook.persistentStore.read("seen");
	if (seen !== req.path) {
		clambhook.log("store mismatch: " + seen);
	}
	$done(req);
}
`,
	}}
	mgr, err := NewManager(modules, t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	req := httptest.NewRequest("GET", "http://example.com/path", nil)
	if _, err := mgr.RunRequestHook(req); err != nil {
		t.Fatalf("run hook: %v", err)
	}

	logs := mgr.ModuleLogs("store-test")
	if len(logs) != 0 {
		t.Fatalf("unexpected logs: %v", logs)
	}
}

func TestModuleSnapshot(t *testing.T) {
	modules := []config.ModuleConfig{{
		Name:    "snap",
		Enabled: true,
		Script:  "function onRequest(req) { $done(req); } function onResponse() {}",
	}}
	mgr, err := NewManager(modules, t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	snap := mgr.ModulesSnapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snap))
	}
	if !snap[0].HasRequest {
		t.Fatal("expected HasRequest")
	}
	if !snap[0].HasResponse {
		t.Fatal("expected HasResponse")
	}
	if snap[0].HasCron {
		t.Fatal("unexpected HasCron")
	}
}
