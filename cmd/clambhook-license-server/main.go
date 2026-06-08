package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/JohnThre/clambhook/internal/license"
)

func main() {
	addr := flag.String("addr", envDefault("CLAMBHOOK_LICENSE_ADDR", "127.0.0.1:9091"), "listen address")
	storePath := flag.String("store", envDefault("CLAMBHOOK_LICENSE_STORE", "license-store.json"), "license store path")
	appID := flag.String("app-id", envDefault("CLAMBHOOK_LICENSE_APP_ID", ""), "Apple app id, for example TEAMID.org.jpfchang.clambhook")
	environment := flag.String("environment", envDefault("CLAMBHOOK_LICENSE_ENVIRONMENT", "production"), "App Attest environment")
	allowNoReceiptRisk := flag.Bool("allow-no-receipt-risk", envBool("CLAMBHOOK_LICENSE_ALLOW_NO_RECEIPT_RISK", false), "allow startup without App Attest receipt risk refresh")
	flag.Parse()

	hmacSecret, err := secretFromEnv("CLAMBHOOK_LICENSE_HMAC_SECRET")
	if err != nil {
		log.Fatal(err)
	}
	grantSecret, err := secretFromEnv("CLAMBHOOK_LICENSE_GRANT_SECRET")
	if err != nil {
		log.Fatal(err)
	}
	store, err := license.NewFileStore(*storePath)
	if err != nil {
		log.Fatal(err)
	}
	appleRoots := []byte(os.Getenv("CLAMBHOOK_LICENSE_APPLE_ROOTS_PEM"))
	if *environment == "production" && len(appleRoots) == 0 {
		log.Fatal("CLAMBHOOK_LICENSE_APPLE_ROOTS_PEM is required in production")
	}
	cfg := license.Config{
		AppID:              *appID,
		Environment:        *environment,
		HMACSecret:         hmacSecret,
		GrantSigningSecret: grantSecret,
		AppleRootsPEM:      appleRoots,
		TrialDuration:      62 * 24 * time.Hour,
		OfflineGrace:       7 * 24 * time.Hour,
		MaxAttestations30d: envInt("CLAMBHOOK_LICENSE_MAX_ATTESTATIONS_30D", 3),
	}
	var receiptRisk license.ReceiptRiskAssessor = license.NoopReceiptRiskAssessor{}
	if pemData := os.Getenv("CLAMBHOOK_LICENSE_DEVICECHECK_PRIVATE_KEY_PEM"); pemData != "" {
		receiptRisk, err = license.NewAppleReceiptRiskAssessor(license.AppleReceiptRiskConfig{
			Endpoint:      envDefault("CLAMBHOOK_LICENSE_APP_ATTEST_RECEIPT_URL", ""),
			TeamID:        os.Getenv("CLAMBHOOK_LICENSE_DEVICECHECK_TEAM_ID"),
			KeyID:         os.Getenv("CLAMBHOOK_LICENSE_DEVICECHECK_KEY_ID"),
			PrivateKeyPEM: []byte(pemData),
		})
		if err != nil {
			log.Fatal(err)
		}
	} else if *environment == "production" && !*allowNoReceiptRisk {
		log.Fatal("DeviceCheck receipt risk credentials are required in production; set -allow-no-receipt-risk only for development")
	}
	server, err := license.NewServer(cfg, store, nil, nil, receiptRisk)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("license server listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, server))
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func secretFromEnv(key string) ([]byte, error) {
	value := os.Getenv(key)
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) >= 32 {
		return decoded, nil
	}
	return []byte(value), nil
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	var out int
	if _, err := fmt.Sscanf(value, "%d", &out); err == nil && out > 0 {
		return out
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	case "0", "false", "FALSE", "no", "NO":
		return false
	default:
		return fallback
	}
}
