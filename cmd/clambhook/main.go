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

	eng := engine.New(cfg)

	srv := api.New(eng)
	if err := srv.Start(*apiAddr); err != nil {
		log.Fatalf("start api: %v", err)
	}

	log.Printf("clambhook %s started", version)

	// Wait for interrupt.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Printf("shutting down...")
	eng.Stop()
	srv.Shutdown(context.Background())
}
