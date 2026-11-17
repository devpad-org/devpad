package wsservice

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// definition is everything devpad knows about one kind of service. Adding a
// service is one entry in definitions, plus its type in the frontend's
// catalog for its label, icon and description.
type definition struct {
	// defaults is the service's configuration before devpad generates its
	// password.
	defaults ServiceConfig
	// dataDir is where the image keeps its data, and so where the
	// service's volume is mounted. Where the image declares a VOLUME, it
	// must be that directory or one the service is told to use instead:
	// Docker mounts an anonymous volume over a declared directory that no
	// mount covers exactly, so data kept there would not be in the
	// service's volume.
	dataDir string
	// env is the sidecar container's environment.
	env func(cfg ServiceConfig) []string
	// connectionEnv is what a workspace is given to reach the service. Nil
	// gives the usual <TYPE>_HOST, _PORT, _USER, _PASSWORD and _DATABASE.
	connectionEnv func(host string, cfg ServiceConfig) []string
	// license, when set, is terms the user must accept before the service
	// is deployed, because its environment accepts them for the image.
	license *License
	// requiredArch, when set, is the only Docker daemon architecture the
	// image supports.
	requiredArch string
}

// License describes terms a user must accept before devpad deploys a
// service: devpad accepts them on the image's behalf (SQL Server's
// ACCEPT_EULA), so the user deploying it has to agree to them first. The
// catalog shows it as sent.
type License struct {
	// Name is the terms' link text, read as "I accept the …".
	Name string `json:"name"`
	URL  string `json:"url"`
	// Notes say what accepting the terms means here.
	Notes []string `json:"notes"`
}

var definitions = map[ServiceType]definition{
	ServicePostgres: {
		defaults: ServiceConfig{Image: "postgres:17", Port: 5432, DefaultUser: "devpad", DefaultDB: "devpad"},
		// PGDATA puts the data here rather than in the image's VOLUME.
		dataDir: "/data",
		env: func(cfg ServiceConfig) []string {
			return []string{
				"POSTGRES_USER=" + cfg.DefaultUser,
				"POSTGRES_PASSWORD=" + cfg.DefaultPass,
				"POSTGRES_DB=" + cfg.DefaultDB,
				"PGDATA=/data/pgdata",
			}
		},
	},
	ServiceMongoDB: {
		defaults: ServiceConfig{Image: "mongo:8", Port: 27017, DefaultUser: "devpad", DefaultDB: "devpad"},
		dataDir:  "/data/db",
		env: func(cfg ServiceConfig) []string {
			return []string{
				"MONGO_INITDB_ROOT_USERNAME=" + cfg.DefaultUser,
				"MONGO_INITDB_ROOT_PASSWORD=" + cfg.DefaultPass,
				"MONGO_INITDB_DATABASE=" + cfg.DefaultDB,
			}
		},
	},
	ServiceMariaDB: {
		defaults: ServiceConfig{Image: "mariadb:11", Port: 3306, DefaultUser: "devpad", DefaultDB: "devpad"},
		dataDir:  "/var/lib/mysql",
		env: func(cfg ServiceConfig) []string {
			return []string{
				"MARIADB_USER=" + cfg.DefaultUser,
				"MARIADB_PASSWORD=" + cfg.DefaultPass,
				"MARIADB_DATABASE=" + cfg.DefaultDB,
				"MARIADB_ROOT_PASSWORD=" + cfg.DefaultPass,
			}
		},
	},
	ServiceCouchDB: {
		defaults: ServiceConfig{Image: "couchdb:3", Port: 5984, DefaultUser: "devpad", DefaultDB: "devpad"},
		dataDir:  "/opt/couchdb/data",
		env: func(cfg ServiceConfig) []string {
			return []string{
				"COUCHDB_USER=" + cfg.DefaultUser,
				"COUCHDB_PASSWORD=" + cfg.DefaultPass,
			}
		},
	},
	ServiceMeilisearch: {
		defaults: ServiceConfig{Image: "getmeili/meilisearch:v1.45", Port: 7700},
		// MEILI_DB_PATH puts the data here rather than in the image's VOLUME.
		dataDir: "/data",
		env: func(cfg ServiceConfig) []string {
			return []string{
				"MEILI_MASTER_KEY=" + cfg.DefaultPass,
				"MEILI_DB_PATH=/data",
				"MEILI_NO_ANALYTICS=true",
			}
		},
		connectionEnv: func(host string, cfg ServiceConfig) []string {
			return []string{
				"MEILISEARCH_HOST=" + host,
				fmt.Sprintf("MEILISEARCH_PORT=%d", cfg.Port),
				fmt.Sprintf("MEILISEARCH_URL=http://%s:%d", host, cfg.Port),
				"MEILISEARCH_MASTER_KEY=" + cfg.DefaultPass,
				"MEILISEARCH_API_KEY=" + cfg.DefaultPass,
				"MEILI_MASTER_KEY=" + cfg.DefaultPass,
			}
		},
	},
	ServiceSQLServer: {
		defaults: ServiceConfig{Image: "mcr.microsoft.com/mssql/server:2025-latest", Port: 1433, DefaultUser: "devpad", DefaultDB: "devpad"},
		// The image's data, logs and secrets all live under here, owned by
		// the image's non-root mssql user.
		dataDir: "/var/opt/mssql",
		env: func(cfg ServiceConfig) []string {
			return []string{
				// Only set once the user deploying it has accepted the
				// licence terms; see startOne.
				"ACCEPT_EULA=Y",
				// The free Enterprise Developer edition, which has no
				// production use rights. Never a paid edition or a product
				// key.
				"MSSQL_PID=EnterpriseDeveloper",
				"MSSQL_SA_PASSWORD=" + cfg.DefaultPass,
				// SQL Server creates this database and login on first start.
				"MSSQL_DB=" + cfg.DefaultDB,
				"MSSQL_USER=" + cfg.DefaultUser,
				"MSSQL_PASSWORD=" + cfg.DefaultPass,
				// SQL Server otherwise takes up to 80% of the host's memory.
				"MSSQL_MEMORY_LIMIT_MB=2048",
			}
		},
		license: &License{
			Name: "Microsoft SQL Server licence terms",
			// The link SQL Server itself prints when its EULA isn't accepted.
			URL: "https://go.microsoft.com/fwlink/?LinkId=746388",
			Notes: []string{
				"Developer edition is for development and testing only, not production.",
				"SQL Server sends usage data to Microsoft, which free editions cannot turn off.",
			},
		},
		// Microsoft publishes SQL Server images for x86-64 only, and
		// supports them under emulation on no other architecture.
		requiredArch: "amd64",
	},
}

func lookupDefinition(t ServiceType) (definition, bool) {
	def, ok := definitions[t]
	return def, ok
}

// ValidServiceType returns true if the given type is supported.
func ValidServiceType(t ServiceType) bool {
	_, ok := definitions[t]
	return ok
}

// SupportedTypes lists every supported service type, sorted.
func SupportedTypes() []string {
	types := make([]string, 0, len(definitions))
	for t := range definitions {
		types = append(types, string(t))
	}
	sort.Strings(types)
	return types
}

// Licenses maps each service type with licence terms to them.
func Licenses() map[ServiceType]License {
	licenses := map[ServiceType]License{}
	for t, def := range definitions {
		if def.license != nil {
			l := *def.license
			l.Notes = slices.Clone(l.Notes)
			licenses[t] = l
		}
	}
	return licenses
}

// connectionVars is what a workspace is given to reach a service of type t
// at host.
func (d definition) connectionVars(t ServiceType, host string, cfg ServiceConfig) []string {
	if d.connectionEnv != nil {
		return d.connectionEnv(host, cfg)
	}
	prefix := strings.ToUpper(string(t))
	return []string{
		fmt.Sprintf("%s_HOST=%s", prefix, host),
		fmt.Sprintf("%s_PORT=%d", prefix, cfg.Port),
		fmt.Sprintf("%s_USER=%s", prefix, cfg.DefaultUser),
		fmt.Sprintf("%s_PASSWORD=%s", prefix, cfg.DefaultPass),
		fmt.Sprintf("%s_DATABASE=%s", prefix, cfg.DefaultDB),
	}
}
