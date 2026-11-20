package preview

import (
	"fmt"
	"strconv"
	"strings"
)

// Config says where previews are served. The service makes preview URLs
// from it and the handler reads the preview back from the host it is asked
// for, so both must be given the same Config.
type Config struct {
	// Domain is the preview domain, under which each preview has its own
	// subdomain, "{workspaceID}-{port}.{Domain}".
	Domain string
	// HTTPSPort is the main server's HTTPS port. A port other than 443 is
	// put in preview URLs.
	HTTPSPort int
}

// url is the address that opens a workspace port's preview with token.
func (c Config) url(workspaceID int64, port int, token string) string {
	host := fmt.Sprintf("%d-%d.%s", workspaceID, port, c.Domain)
	if c.HTTPSPort != 0 && c.HTTPSPort != 443 {
		host = fmt.Sprintf("%s:%d", host, c.HTTPSPort)
	}
	return fmt.Sprintf("https://%s?token=%s", host, token)
}

// parseHost extracts the workspace ID and port from a preview host like
// "3-8080.preview.example.com".
func (c Config) parseHost(host string) (int64, int, error) {
	// Strip port number from host if present.
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	suffix := "." + c.Domain
	if !strings.HasSuffix(host, suffix) {
		return 0, 0, fmt.Errorf("host %q does not match preview domain", host)
	}

	prefix := strings.TrimSuffix(host, suffix)
	parts := strings.SplitN(prefix, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid subdomain format: %q", prefix)
	}

	wsID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid workspace id in subdomain: %w", err)
	}

	port, err := strconv.Atoi(parts[1])
	if err != nil || port < 1 || port > 65535 {
		return 0, 0, fmt.Errorf("invalid port in subdomain: %q", parts[1])
	}

	return wsID, port, nil
}
