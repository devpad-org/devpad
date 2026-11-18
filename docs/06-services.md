---
title: Services
summary: Databases and search engines that run beside a workspace, and how a project gives every sandbox its own copy.
---

A service is a database or search engine running in its own container next to a workspace, on the workspace's private network. Each workspace has its own copy with its own data: no two workspaces, sandboxes included, ever share one.

## The catalog {#catalog}

| Service | Port | Notes |
|---|---|---|
| PostgreSQL | 5432 | Relational database. |
| MongoDB | 27017 | Document database with an admin-authenticated default database. |
| MariaDB | 3306 | MySQL-compatible relational database. |
| CouchDB | 5984 | HTTP document database. |
| Meilisearch | 7700 | Full-text search engine with a workspace-local master key. |
| SQL Server | 1433 | Microsoft SQL Server 2025, free Developer edition. Needs 2 GB of memory and an amd64 Docker host; [licence terms](#licence-terms) apply. |

A workspace has at most one service of each type. The user and database are `devpad`, and each copy gets a password of its own. Its data lives in a Docker volume that belongs to that copy alone.

## Services in a workspace {#workspace-services}

Open **Database Services** from the IDE's activity bar (**Services** in the mobile More menu). The **Service Catalog** lists what you can deploy. Press **Deploy** to add one to this workspace: it starts at once if the workspace is running, or with the workspace otherwise.

**Workspace Services** shows each service's host, port, credentials and **Connection URL**, each with a copy button, and lets you **Start**, **Stop** or remove it. Removing a service deletes its data volume, and can't be undone.

## Connection variables {#connection-variables}

Every new terminal and command in the workspace gets each service's connection details as environment variables: `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE`, and the same with `MONGODB_`, `MARIADB_`, `COUCHDB_` and `SQLSERVER_`. Meilisearch gets `MEILISEARCH_HOST`, `MEILISEARCH_PORT`, `MEILISEARCH_URL`, `MEILISEARCH_MASTER_KEY`, `MEILISEARCH_API_KEY` and `MEILI_MASTER_KEY`.

When you add or remove a service, devpad updates the variables new terminals get straight away, without restarting the workspace. Terminals opened before the change still have the old values: open a new one. `DEVPAD_SERVICE_VARS` lists the names of every service variable the workspace's container was made with.

AI agents are told which variables exist, never their values, so a password doesn't end up in a transcript unless an agent reads it from the environment.

## Project services {#project-services}

A project's services, set in the **Services** section of the Project panel, are the ones every new workspace of the project gets: each task and review sandbox starts with its own empty copy of each, which stops and is deleted with the sandbox.

- Pick one from **Add a service…** and press **Add**. Adding a service to the project doesn't change workspaces that already exist.
- Your project workspace gets the project's services when you [rebuild](/docs/projects#rebuilding) it. To add one without rebuilding, the Project panel says which of the project's services your workspace doesn't have yet and offers **Add to workspace**.
- The other way round: if your project workspace has a service the project doesn't, **Workspace Services** offers **Give sandboxes a copy**, which adds it to the project.
- Removing a service from the project only affects new workspaces: existing ones keep their copies.

## Waiting for services {#readiness}

Each service has a health check. A new workspace's [setup script](/docs/projects#setup-script) waits until every service passes it, for up to 4 minutes, so the script can create tables or load fixtures straight away.

If a service doesn't get ready (it exits, stops, or never accepts connections), a task or review fails with a message saying which service and why; starting the task again retries. In your project workspace, the setup run is marked as failed, and you can run it again from the Project panel.

## Licence terms {#licence-terms}

SQL Server is the one service with licence terms: Developer edition is for development and testing only, not production, and it sends usage data to Microsoft that free editions can't turn off. Deploying it asks you to accept the terms each time. Accepting them when adding it to a project accepts them for every copy devpad deploys for that project.

If the terms change, devpad refuses new copies until you accept the new ones: remove the service from the project's services and add it again.
