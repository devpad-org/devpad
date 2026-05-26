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
	default:
		return nil
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
