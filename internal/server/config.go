package server

// Config holds server configuration.
type Config struct {
	Port             int
	Domain           string // Base domain; if set, enables HTTPS via Let's Encrypt.
	CFAPIToken       string // Cloudflare API token for DNS-01 challenge. Required if Domain is set.
	HTTPRedirectPort int    // Port for HTTP→HTTPS redirect server. Requires Domain.
	DBPath           string
	PreviewDomain    string // e.g. "preview.example.com" — previews served on {wsID}-{port}.preview.example.com
	EncryptionKey    string // Hex-encoded 32-byte key for encrypting secrets (SSH private keys). Required.
}
