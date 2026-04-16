package server

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

// setupTLS configures certmagic with Cloudflare DNS-01 challenge and returns
// a tls.Config for the given domain. Certificates are automatically obtained
// and renewed.
// setupTLS configures certmagic with Cloudflare DNS-01 challenge and returns
// a tls.Config for the given domains. If previewDomain is non-empty, a wildcard
// certificate for *.previewDomain is also obtained.
func setupTLS(ctx context.Context, domain, previewDomain, cfAPIToken string) (*tls.Config, error) {
	magic := certmagic.NewDefault()

	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		CA:     certmagic.LetsEncryptProductionCA,
		Agreed: true,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: &cloudflare.Provider{
					APIToken: cfAPIToken,
				},
			},
		},
		DisableHTTPChallenge:    true,
		DisableTLSALPNChallenge: true,
	})
	magic.Issuers = []certmagic.Issuer{issuer}

	domains := []string{domain}
	if previewDomain != "" {
		domains = append(domains, "*."+previewDomain)
	}

	if err := magic.ManageSync(ctx, domains); err != nil {
		return nil, fmt.Errorf("managing TLS certificates: %w", err)
	}

	tlsCfg := magic.TLSConfig()
	tlsCfg.NextProtos = append([]string{"h2", "http/1.1"}, tlsCfg.NextProtos...)

	return tlsCfg, nil
}
