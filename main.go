package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/devpad-org/devpad/internal/server"
)

func main() {
	port := flag.Int("port", 0, "Port the web server listens on (env: DEVPAD_PORT, default: 443 with --domain, 8080 without)")
	domain := flag.String("domain", "", "Base domain for HTTPS via Let's Encrypt (env: DEVPAD_DOMAIN)")
	cfAPIToken := flag.String("cf-api-token", "", "Cloudflare API token for DNS-01 challenge (env: DEVPAD_CF_API_TOKEN, required with --domain)")
	httpRedirectPort := flag.Int("http-redirect-port", 0, "Port for HTTP→HTTPS redirect server (env: DEVPAD_HTTP_REDIRECT_PORT, requires --domain)")
	dbPath := flag.String("db-path", "", "Path to the SQLite database file (env: DEVPAD_DB_PATH, default: devpad.db)")
	previewDomain := flag.String("preview-domain", "", "Domain for workspace previews, e.g. preview.example.com (env: DEVPAD_PREVIEW_DOMAIN)")
	flag.Parse()

	// Environment variable fallbacks (flags take precedence).
	envFallbackString(domain, "DEVPAD_DOMAIN")
	envFallbackString(cfAPIToken, "DEVPAD_CF_API_TOKEN")
	envFallbackString(dbPath, "DEVPAD_DB_PATH")
	envFallbackString(previewDomain, "DEVPAD_PREVIEW_DOMAIN")
	envFallbackInt(port, "DEVPAD_PORT")
	envFallbackInt(httpRedirectPort, "DEVPAD_HTTP_REDIRECT_PORT")

	if *dbPath == "" {
		*dbPath = "devpad.db"
	}

	// Smart defaults based on whether domain is set.
	if *port == 0 {
		if *domain != "" {
			*port = 443
		} else {
			*port = 8080
		}
	}

	// Validation.
	if *domain != "" && *cfAPIToken == "" {
		log.Fatal("--cf-api-token (or DEVPAD_CF_API_TOKEN) is required when --domain is set")
	}
	if *httpRedirectPort != 0 && *domain == "" {
		log.Fatal("--http-redirect-port requires --domain to be set")
	}

	cfg := server.Config{
		Port:             *port,
		Domain:           *domain,
		CFAPIToken:       *cfAPIToken,
		HTTPRedirectPort: *httpRedirectPort,
		DBPath:           *dbPath,
		PreviewDomain:    *previewDomain,
	}

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}

	log.Println("server stopped")
}

func envFallbackString(dst *string, key string) {
	if *dst == "" {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
}

func envFallbackInt(dst *int, key string) {
	if *dst == 0 {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
}
