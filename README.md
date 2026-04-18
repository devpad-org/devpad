# Devpad

A web-based IDE that connects to Docker-based workspaces.

## Quick Start

```bash
# Build
make build

# Run in development mode (HTTP on port 8080)
./bin/devpad
```

## Configuration

Devpad is configured via command-line flags and/or environment variables. Flags take precedence over environment variables.

### Flags & Environment Variables

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--port` | `DEVPAD_PORT` | `8080` (HTTP) / `443` (HTTPS) | Port the web server listens on |
| `--domain` | `DEVPAD_DOMAIN` | — | Base domain. Enables HTTPS via Let's Encrypt when set |
| `--cf-api-token` | `DEVPAD_CF_API_TOKEN` | — | Cloudflare API token for DNS-01 challenge. **Required** if `--domain` is set |
| `--http-redirect-port` | `DEVPAD_HTTP_REDIRECT_PORT` | — | Port for HTTP→HTTPS redirect server. Requires `--domain` |
| `--db-path` | `DEVPAD_DB_PATH` | `devpad.db` | Path to the SQLite database file |
| `--preview-domain` | `DEVPAD_PREVIEW_DOMAIN` | — | Domain for workspace previews (e.g. `preview.example.com`) |
| `--encryption-key` | `DEVPAD_ENCRYPTION_KEY` | — | Hex-encoded 32-byte key for encrypting secrets (SSH private keys). Auto-generated if not set |

### Examples

**Development** — plain HTTP on the default port:

```bash
./bin/devpad
# Listening on :8080
```

**Development** — custom port:

```bash
./bin/devpad --port 3000
# or
DEVPAD_PORT=3000 ./bin/devpad
```

**Production** — HTTPS with automatic Let's Encrypt certificates:

```bash
./bin/devpad \
  --domain devpad.example.com \
  --cf-api-token "$CF_API_TOKEN" \
  --preview-domain preview.example.com
# Listening on :443 (HTTPS)
# Certs obtained for devpad.example.com + *.preview.example.com
```

**Production** — HTTPS with HTTP→HTTPS redirect:

```bash
./bin/devpad \
  --domain devpad.example.com \
  --cf-api-token "$CF_API_TOKEN" \
  --preview-domain preview.example.com \
  --http-redirect-port 80
# HTTPS on :443, HTTP redirect on :80
```

**Production** — non-standard ports:

```bash
./bin/devpad \
  --domain devpad.example.com \
  --cf-api-token "$CF_API_TOKEN" \
  --preview-domain preview.example.com \
  --port 8443 \
  --http-redirect-port 8080
# Preview URLs will include the port: https://1-3000.preview.example.com:8443
```

### HTTPS / Let's Encrypt

When `--domain` is set, Devpad automatically obtains and renews TLS certificates from Let's Encrypt using the **DNS-01 challenge** via the Cloudflare API. This means:

- The server does **not** need port 80 open for certificate validation
- Certificates are stored in `$HOME/.local/share/certmagic` by default
- Renewal is fully automatic
- If `--preview-domain` is also set, a wildcard certificate (`*.preview.example.com`) is obtained alongside the base domain certificate

You need a Cloudflare API token with **Zone → DNS → Edit** permissions for the zone(s) containing your domain and preview domain. Create one at [Cloudflare API Tokens](https://dash.cloudflare.com/profile/api-tokens).

> **Tip:** On Linux, to bind ports 80/443 without root:
> ```bash
> sudo setcap cap_net_bind_service=+ep ./bin/devpad
> ```

### Encryption Key

Devpad encrypts sensitive data at rest (SSH private keys) using AES-256-GCM. An encryption key is required for production use.

Generate a key:

```bash
openssl rand -hex 32
```

Then pass it via flag or environment variable:

```bash
./bin/devpad --encryption-key "your-64-char-hex-key"
# or
DEVPAD_ENCRYPTION_KEY="your-64-char-hex-key" ./bin/devpad
```

If no key is configured, Devpad generates an ephemeral key on startup and logs it. **Save this key** — without it, previously encrypted data (SSH private keys) cannot be decrypted after a restart.

### SSH Keys

Each user can generate an ED25519 SSH key pair from the **Settings** page. The private key is encrypted and stored in the database; the public key is shown in the UI for users to copy to their Git hosting provider (GitHub, GitLab, Bitbucket).

When a workspace starts, Devpad automatically injects the user's SSH key, a `known_hosts` file for common Git hosts, and an SSH config into the container at `~/.ssh/`. This enables `git push`/`pull` over SSH without any manual setup inside workspaces.

## License

See [LICENSE](LICENSE).
