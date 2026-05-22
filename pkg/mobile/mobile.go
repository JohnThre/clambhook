// Package mobile exposes clambhook's core runtime to Android via gomobile.
package mobile

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/api"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/logstream"

	// Register all protocols for embedded Android builds.
	_ "github.com/JohnThre/clambhook/internal/protocol/openvpn"
	_ "github.com/JohnThre/clambhook/internal/protocol/reality"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
	_ "github.com/JohnThre/clambhook/internal/protocol/tor"
	_ "github.com/JohnThre/clambhook/internal/protocol/trojan"
	_ "github.com/JohnThre/clambhook/internal/protocol/vless"
	_ "github.com/JohnThre/clambhook/internal/protocol/vmess"
	_ "github.com/JohnThre/clambhook/internal/protocol/wireguard"
)

const defaultAPIAddr = "127.0.0.1:9090"

var (
	runtimeMu sync.Mutex
	active    *runtime
)

type runtime struct {
	eng *engine.Engine
	bus *events.Bus
	srv *api.Server
}

// Start launches the embedded API server. The actual proxy listeners are still
// controlled through the API's connect/disconnect endpoints.
func Start(configPath, apiAddr, apiToken string) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	if active != nil {
		return nil
	}

	apiAddr = normalizeAPIAddr(apiAddr)
	cfg, err := loadConfig(configPath, apiAddr)
	if err != nil {
		return err
	}
	if err := api.ValidateAuthConfig(apiAddr, apiToken); err != nil {
		return fmt.Errorf("api auth: %w", err)
	}

	bus := events.NewBus(events.DefaultConfig())
	log.SetOutput(io.MultiWriter(os.Stderr, logstream.NewWriter(bus)))
	eng := engine.New(cfg, bus)
	srv := api.NewWithOptions(eng, bus, api.Options{AuthToken: strings.TrimSpace(apiToken)})
	if err := srv.Start(apiAddr); err != nil {
		eng.Stop()
		if closeErr := eng.CloseGeo(); closeErr != nil {
			log.Printf("close geo after API start failure: %v", closeErr)
		}
		bus.Close()
		return fmt.Errorf("start api: %w", err)
	}

	active = &runtime{eng: eng, bus: bus, srv: srv}
	log.Printf("clambhook mobile runtime started")
	return nil
}

// Stop shuts down the embedded API server and any active proxy listeners.
func Stop() error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	if active == nil {
		return nil
	}

	rt := active
	active = nil

	var firstErr error
	if err := rt.eng.Stop(); err != nil {
		firstErr = err
	}
	if err := rt.eng.CloseGeo(); err != nil && firstErr == nil {
		firstErr = err
	}
	rt.bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.srv.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	log.Printf("clambhook mobile runtime stopped")
	return firstErr
}

// Reload validates and applies the TOML configuration at configPath.
func Reload(configPath string) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	if active == nil {
		return fmt.Errorf("clambhook mobile runtime is not running")
	}
	cfg, err := loadConfig(configPath, defaultAPIAddr)
	if err != nil {
		return err
	}
	return active.eng.Reload(cfg)
}

// IsRunning reports whether the embedded runtime has been started.
func IsRunning() bool {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	return active != nil
}

// ValidateConfig parses configPath and returns an error for invalid TOML.
func ValidateConfig(configPath string) error {
	_, err := loadConfig(configPath, defaultAPIAddr)
	return err
}

func loadConfig(configPath, apiAddr string) (*config.Config, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath != "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	}
	return defaultConfig(normalizeAPIAddr(apiAddr)), nil
}

func normalizeAPIAddr(apiAddr string) string {
	apiAddr = strings.TrimSpace(apiAddr)
	if apiAddr == "" {
		return defaultAPIAddr
	}
	return strings.TrimPrefix(strings.TrimPrefix(apiAddr, "http://"), "https://")
}

func defaultConfig(apiAddr string) *config.Config {
	return &config.Config{
		Active: "default",
		Profiles: []config.Profile{
			{
				Name: "default",
				Listen: config.ListenConfig{
					SOCKS5: "127.0.0.1:1080",
					HTTP:   "127.0.0.1:8080",
				},
				API: config.APIConfig{
					Listen: apiAddr,
				},
			},
		},
	}
}
