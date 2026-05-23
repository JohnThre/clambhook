package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

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

func main() {
	configPath := flag.String("config", "", "path to config file")
	apiAddr := flag.String("api", "127.0.0.1:9090", "API listen address")
	apiToken := flag.String("api-token", os.Getenv("CLAMBHOOK_API_TOKEN"), "bearer token required for API access")
	noWatch := flag.Bool("no-watch", false, "disable config file hot-reload")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

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

	bus := events.NewBus(events.DefaultConfig())
	log.SetOutput(io.MultiWriter(os.Stderr, logstream.NewWriter(bus)))
	eng := engine.New(cfg, bus)
	trafficStore, err := traffic.NewStore(cfg.Traffic, func(address string) (*geo.Location, error) {
		return eng.GeoReader().Lookup(address)
	})
	if err != nil {
		log.Fatalf("traffic: %v", err)
	}

	if err := api.ValidateAuthConfig(*apiAddr, *apiToken); err != nil {
		log.Fatalf("api auth: %v", err)
	}

	trafficCtx, trafficCancel := context.WithCancel(context.Background())
	defer trafficCancel()
	trafficStore.Start(trafficCtx, bus)

	srv := api.NewWithOptions(eng, bus, api.Options{AuthToken: *apiToken, TrafficStore: trafficStore})
	if err := srv.Start(*apiAddr); err != nil {
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
			if trafficStore != nil {
				if err := trafficStore.Reconfigure(next.Traffic); err != nil {
					log.Printf("traffic reload: %v", err)
				}
			}
			return eng.Reload(next)
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
	if err := eng.CloseGeo(); err != nil {
		log.Printf("close geo: %v", err)
	}
	bus.Close()
	srv.Shutdown(context.Background())
}
