package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JohnThre/clambhook/internal/api"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/logstream"
	"github.com/JohnThre/clambhook/internal/traffic"
	"github.com/JohnThre/clambhook/internal/watcher"

	// Register all protocols.
	_ "github.com/JohnThre/clambhook/internal/protocol/openvpn"
	_ "github.com/JohnThre/clambhook/internal/protocol/reality"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
	_ "github.com/JohnThre/clambhook/internal/protocol/tor"
	_ "github.com/JohnThre/clambhook/internal/protocol/trojan"
	_ "github.com/JohnThre/clambhook/internal/protocol/vless"
	_ "github.com/JohnThre/clambhook/internal/protocol/vmess"
	_ "github.com/JohnThre/clambhook/internal/protocol/wireguard"
)

var version = "dev"

const apiShutdownTimeout = 5 * time.Second

func main() {
	configPath := flag.String("config", "", "path to config file")
	apiAddr := flag.String("api", "127.0.0.1:9090", "API listen address")
	apiToken := flag.String("api-token", os.Getenv("CLAMBHOOK_API_TOKEN"), "bearer token required for API access")
	noWatch := flag.Bool("no-watch", false, "disable config file hot-reload")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	apiAddrExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "api" {
			apiAddrExplicit = true
		}
	})

	if *showVersion {
		fmt.Printf("clambhook %s\n", version)
		os.Exit(0)
	}

	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.Load(*configPath)
		if err != nil {
			log.Fatalf("load config: %v", err)
		}
	} else {
		cfg = &config.Config{
			Active: "default",
			Profiles: []config.Profile{
				{
					Name: "default",
					Listen: config.ListenConfig{
						SOCKS5: "127.0.0.1:1080",
					},
					API: config.APIConfig{
						Listen: *apiAddr,
					},
				},
			},
			Traffic: config.DefaultTrafficConfig(),
		}
	}
	resolvedAPIAddr := resolveAPIListen(cfg, *apiAddr, apiAddrExplicit)

	bus := events.NewBus(events.DefaultConfig())
	log.SetOutput(io.MultiWriter(os.Stderr, logstream.NewWriter(bus)))
	eng := engine.New(cfg, bus)
	trafficMgr, err := traffic.NewManager(cfg.Traffic, func(address string) (*geo.Location, error) {
		return eng.GeoReader().Lookup(address)
	})
	if err != nil {
		log.Fatalf("traffic: %v", err)
	}

	if err := api.ValidateAuthConfig(resolvedAPIAddr, *apiToken); err != nil {
		log.Fatalf("api auth: %v", err)
	}

	trafficMgr.Start(context.Background(), bus)
	defer trafficMgr.Stop()

	if err := eng.Start(context.Background()); err != nil {
		log.Fatalf("start engine: %v", err)
	}

	srv := api.NewWithOptions(eng, bus, api.Options{AuthToken: *apiToken, TrafficStore: trafficMgr.Store()})
	if err := srv.Start(resolvedAPIAddr); err != nil {
		log.Fatalf("start api: %v", err)
	}

	// Wait for interrupt.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Watch the config file for changes. No-op when -config is empty (the
	// inline default profile has nothing to watch) or -no-watch is set.
	var cfgWatcher *watcher.Watcher
	if *configPath != "" && !*noWatch {
		var err error
		cfgWatcher, err = watcher.New(*configPath, func(next *config.Config) error {
			if err := eng.Reload(next); err != nil {
				return err
			}
			nextAPIAddr := resolveAPIListen(next, *apiAddr, apiAddrExplicit)
			if nextAPIAddr != resolvedAPIAddr {
				log.Printf("api listen changed from %s to %s in config; restart required to rebind API",
					resolvedAPIAddr, nextAPIAddr)
			}
			if err := trafficMgr.Reconfigure(next.Traffic); err != nil {
				log.Printf("traffic reload: %v", err)
			}
			srv.SetTrafficStore(trafficMgr.Store())
			return nil
		}, bus)
		if err != nil {
			log.Fatalf("init config watcher: %v", err)
		}
		if err := cfgWatcher.Start(ctx); err != nil {
			log.Fatalf("start config watcher: %v", err)
		}
		log.Printf("watching %s for changes", *configPath)
	}

	log.Printf("clambhook %s started", version)

	<-ctx.Done()

	log.Printf("shutting down...")
	if cfgWatcher != nil {
		if err := cfgWatcher.Stop(); err != nil {
			log.Printf("stop watcher: %v", err)
		}
	}
	eng.Stop()
	trafficMgr.Stop()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown api: %v", err)
	}
	if err := eng.CloseGeo(); err != nil {
		log.Printf("close geo: %v", err)
	}
	bus.Close()
}

func resolveAPIListen(cfg *config.Config, fallback string, explicit bool) string {
	fallback = strings.TrimSpace(fallback)
	if explicit {
		return fallback
	}
	if cfg != nil {
		if profile, err := cfg.ActiveProfile(); err == nil {
			if listen := strings.TrimSpace(profile.API.Listen); listen != "" {
				return listen
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return "127.0.0.1:9090"
}
