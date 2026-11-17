package wsservice

import (
	"fmt"
	"strings"
)

func sidecarContainerName(workspaceID int64, serviceType ServiceType) string {
	return fmt.Sprintf("devpad-svc-%d-%s", workspaceID, serviceType)
}

func sidecarEnv(serviceType ServiceType, cfg ServiceConfig) []string {
	switch serviceType {
	case ServicePostgres:
		return []string{
			fmt.Sprintf("POSTGRES_USER=%s", cfg.DefaultUser),
			fmt.Sprintf("POSTGRES_PASSWORD=%s", cfg.DefaultPass),
			fmt.Sprintf("POSTGRES_DB=%s", cfg.DefaultDB),
			"PGDATA=/data/pgdata",
		}
	case ServiceMongoDB:
		return []string{
			fmt.Sprintf("MONGO_INITDB_ROOT_USERNAME=%s", cfg.DefaultUser),
			fmt.Sprintf("MONGO_INITDB_ROOT_PASSWORD=%s", cfg.DefaultPass),
			fmt.Sprintf("MONGO_INITDB_DATABASE=%s", cfg.DefaultDB),
		}
	case ServiceMariaDB:
		return []string{
			fmt.Sprintf("MARIADB_USER=%s", cfg.DefaultUser),
			fmt.Sprintf("MARIADB_PASSWORD=%s", cfg.DefaultPass),
			fmt.Sprintf("MARIADB_DATABASE=%s", cfg.DefaultDB),
			fmt.Sprintf("MARIADB_ROOT_PASSWORD=%s", cfg.DefaultPass),
		}
	case ServiceCouchDB:
		return []string{
			fmt.Sprintf("COUCHDB_USER=%s", cfg.DefaultUser),
			fmt.Sprintf("COUCHDB_PASSWORD=%s", cfg.DefaultPass),
		}
	case ServiceMeilisearch:
		return []string{
			fmt.Sprintf("MEILI_MASTER_KEY=%s", cfg.DefaultPass),
			"MEILI_DB_PATH=/data",
			"MEILI_NO_ANALYTICS=true",
		}
	case ServiceSQLServer:
		return []string{
			// Only set once the user deploying it has accepted the licence
			// terms; see startOne.
			"ACCEPT_EULA=Y",
			// The free Enterprise Developer edition, which has no production
			// use rights. Never a paid edition or a product key.
			"MSSQL_PID=EnterpriseDeveloper",
			fmt.Sprintf("MSSQL_SA_PASSWORD=%s", cfg.DefaultPass),
			// SQL Server creates this database and login on first start.
			fmt.Sprintf("MSSQL_DB=%s", cfg.DefaultDB),
			fmt.Sprintf("MSSQL_USER=%s", cfg.DefaultUser),
			fmt.Sprintf("MSSQL_PASSWORD=%s", cfg.DefaultPass),
			// SQL Server otherwise takes up to 80% of the host's memory.
			"MSSQL_MEMORY_LIMIT_MB=2048",
		}
	default:
		return nil
	}
}

// sidecarDataDir is where a service's image keeps its data, and so where its
// volume is mounted. It must be the directory the image declares as a
// VOLUME, when it declares one: Docker mounts an anonymous volume over any
// such directory a mount does not cover exactly, and the data would live
// there instead.
func sidecarDataDir(serviceType ServiceType) string {
	switch serviceType {
	case ServiceMongoDB:
		return "/data/db"
	case ServiceMariaDB:
		return "/var/lib/mysql"
	case ServiceCouchDB:
		return "/opt/couchdb/data"
	case ServiceSQLServer:
		// The image's data, logs and secrets all live under here, owned
		// by the image's non-root mssql user.
		return "/var/opt/mssql"
	default:
		// Postgres (PGDATA) and Meilisearch (MEILI_DB_PATH) are told to
		// keep their data here.
		return "/data"
	}
}

func serviceConnectionEnv(serviceType ServiceType, host string, cfg ServiceConfig) []string {
	prefix := strings.ToUpper(string(serviceType))
	if serviceType == ServiceMeilisearch {
		url := fmt.Sprintf("http://%s:%d", host, cfg.Port)
		return []string{
			fmt.Sprintf("%s_HOST=%s", prefix, host),
			fmt.Sprintf("%s_PORT=%d", prefix, cfg.Port),
			fmt.Sprintf("%s_URL=%s", prefix, url),
			fmt.Sprintf("%s_MASTER_KEY=%s", prefix, cfg.DefaultPass),
			fmt.Sprintf("%s_API_KEY=%s", prefix, cfg.DefaultPass),
			fmt.Sprintf("MEILI_MASTER_KEY=%s", cfg.DefaultPass),
		}
	}

	return []string{
		fmt.Sprintf("%s_HOST=%s", prefix, host),
		fmt.Sprintf("%s_PORT=%d", prefix, cfg.Port),
		fmt.Sprintf("%s_USER=%s", prefix, cfg.DefaultUser),
		fmt.Sprintf("%s_PASSWORD=%s", prefix, cfg.DefaultPass),
		fmt.Sprintf("%s_DATABASE=%s", prefix, cfg.DefaultDB),
	}
}
