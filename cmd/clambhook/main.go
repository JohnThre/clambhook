package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/clambhook/clambhook/internal/api"
	"github.com/clambhook/clambhook/internal/config"
	"github.com/clambhook/clambhook/internal/engine"
	"github.com/clambhook/clambhook/internal/events"
	"github.com/clambhook/clambhook/internal/watcher"

	// Register all protocols.
	_ "github.com/clambhook/clambhook/internal/protocol/openvpn"
	_ "github.com/clambhook/clambhook/internal/protocol/reality"
	_ "github.com/clambhook/clambhook/internal/protocol/shadowsocks"
	_ "github.com/clambhook/clambhook/internal/protocol/tor"
	_ "github.com/clambhook/clambhook/internal/protocol/trojan"
	_ "github.com/clambhook/clambhook/internal/protocol/vless"
	_ "github.com/clambhook/clambhook/internal/protocol/vmess"
	_ "github.com/clambhook/clambhook/internal/protocol/wireguard"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to config file")
	apiAddr := flag.String("api", "127.0.0.1:9090", "API listen address")
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
		}
	}

	bus := events.NewBus(events.DefaultConfig())
	eng := engine.New(cfg, bus)

	srv := api.New(eng, bus)
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
		cfgWatcher, err = watcher.New(*configPath, eng.Reload, bus)
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
