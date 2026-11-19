---
title: Administration
summary: Running a devpad server, and managing its users, invites and workspace resource limits.
audience: admin
---

This page is for admins: it covers what only an admin can do in the app, and how to run the server. Only admins see it.

## Users {#users}

**Admin > User Management** lists every account. From there you can:

- **+ New User**: create an account with a username, email and password, optionally an admin.
- **Edit**: change a user's username, email or admin role. You can't change your own admin role.
- **Reset PW**: set a new password for a user.
- **Delete**: delete a user and their workspaces and projects. You can't delete yourself.

Passwords need at least 8 characters. A user's username and email are also their [Git identity](/docs/getting-started#git-identity) unless they set their own, so neither may start with `-`.

## Invites {#invites}

**Invite User** makes a one-time link someone can use to create their own account, choosing their own username and email. Pick how long it lasts (1, 7 or 30 days) and whether the account is an admin, then send the link yourself: it's shown only once.

- The link looks like `https://devpad.example.com/invite#…`. Only a hash of it is stored, so devpad can't show it again.
- It works once, and not in a browser that's signed in to another account.
- **Pending Invites** lists links nobody has used yet. **Revoke** stops one working.
- An admin's unused invites are revoked when they stop being an admin or are deleted.

## Workspace resource limits {#resource-limits}

**Admin > Workspaces** lists every workspace with its owner, status, memory and CPU. **Limits** changes a workspace's memory (in GB) and CPU cores; changes apply to running containers immediately.

## Running the server {#running-the-server}

Devpad is a single binary that needs Docker. Configure it with flags or environment variables (flags win):

| Flag | Environment | Purpose |
|---|---|---|
| `--encryption-key` | `DEVPAD_ENCRYPTION_KEY` | **Required.** 32-byte hex key that encrypts SSH and AI provider keys. |
| `--port` | `DEVPAD_PORT` | Port to listen on: 8080 by default, 443 with `--domain`. |
| `--db-path` | `DEVPAD_DB_PATH` | The SQLite database, `devpad.db` by default. |
| `--domain` | `DEVPAD_DOMAIN` | Serve HTTPS on this domain, with Let's Encrypt certificates. |
| `--cf-api-token` | `DEVPAD_CF_API_TOKEN` | Cloudflare API token for the DNS-01 challenge. Required with `--domain`. |
| `--http-redirect-port` | `DEVPAD_HTTP_REDIRECT_PORT` | Also redirect HTTP on this port to HTTPS. |
| `--preview-domain` | `DEVPAD_PREVIEW_DOMAIN` | Domain for workspace [previews](/docs/projects#previews). |
| | `DEVPAD_CONTAINER_ID` | Devpad's own container, when it runs in one and can't detect it. |

## The encryption key {#encryption-key}

Make the key once with `openssl rand -hex 32` and keep it safe. Data encrypted with it can't be read with any other key: if it's lost or changed, every user has to generate a new SSH key and enter their AI provider keys again.

## HTTPS and previews {#https-and-previews}

With `--domain`, devpad gets and renews certificates from Let's Encrypt through Cloudflare's DNS API, so port 80 needn't be open. The token needs **Zone → DNS → Edit** for the zones of the domain and the preview domain.

Previews need `--preview-domain` and a wildcard DNS record for it. Each preview is served from `{workspace}-{port}.{preview domain}`, and with `--domain` devpad also gets a wildcard certificate for it. Setting it makes session cookies `Secure`, so devpad must then be served over HTTPS.

Previews show whatever a workspace serves, AI agents' work included, so devpad treats them as hostile pages. The safest setup puts them on a separate registrable domain from the app (`--domain devpad.example.com --preview-domain example-preview.net`), so browsers treat them as a different site. `--domain` may not be the preview domain or under it.

## Running devpad in a container {#in-a-container}

Each workspace has its own Docker network, and devpad reaches workspaces over them. When devpad itself runs in a container it joins each workspace's network as needed, which needs it to know its own container. It detects this itself; if a workspace start logs a warning that it couldn't, set `DEVPAD_CONTAINER_ID` to the container's ID or name.

## Workspace images {#images}

Devpad builds `devpad-workspace:latest`, the image every workspace starts from, when it starts and the image is out of date. Project [environments](/docs/projects#environment) are built on top of it as `devpad-project-<id>:<hash>`, on a network of their own with memory, CPU and time limits.
