package wsservice

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// definition is everything devpad knows about one kind of service. Adding a
// service is one entry in definitions; the frontend's serviceCatalog gives
// it a label, icon and description (TestServiceCatalogMatchesFrontend keeps
// the two in step).
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
	// env is the sidecar container's environment. It never accepts licence
	// terms: that is the license's acceptEnv, which containerEnv adds only
	// once a user has accepted them.
	env func(cfg ServiceConfig) []string
	// connectionEnv is what a workspace is given to reach the service. Nil
	// gives the usual <TYPE>_HOST, _PORT, _USER, _PASSWORD and _DATABASE.
	connectionEnv func(host string, cfg ServiceConfig) []string
	// connection is what the IDE shows a client to reach the service with.
	connection func(host string, cfg ServiceConfig) Connection
	// credentials are the names and secrets the IDE shows beside the
	// connection. Nil gives the service's user, password and database.
	credentials func(cfg ServiceConfig) []Credential
	// license, when set, is terms the user must accept before the service
	// is deployed, because its container accepts them for the image.
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
	// acceptEnv is what accepts the terms for the image. It is added to the
	// container's environment only for a service whose config records a
	// user accepting them, and never sent to the browser.
	acceptEnv []string
}

// Connection is what a client reaches a service with, as the IDE shows it.
type Connection struct {
	// Label names the value, such as "Connection URL".
	Label string `json:"label"`
	Value string `json:"value"`
}

// Credential is one name or secret a client needs, shown on its own.
type Credential struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// CatalogEntry is one service type a workspace can deploy, as the catalog
// shows it.
type CatalogEntry struct {
	Type ServiceType `json:"type"`
	// License, when set, is the terms a user must accept to deploy it.
	License *License `json:"license,omitempty"`
	// Unavailable, when set, is why this Docker host cannot run it.
	Unavailable string `json:"unavailable,omitempty"`
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
		connection: func(host string, cfg ServiceConfig) Connection {
			return connectionURL(fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", cfg.DefaultUser, cfg.DefaultPass, host, cfg.Port, cfg.DefaultDB))
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
		connection: func(host string, cfg ServiceConfig) Connection {
			// The user is a root user, so it authenticates against admin.
			return connectionURL(fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=admin", cfg.DefaultUser, cfg.DefaultPass, host, cfg.Port, cfg.DefaultDB))
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
		connection: func(host string, cfg ServiceConfig) Connection {
			return connectionURL(fmt.Sprintf("mysql://%s:%s@%s:%d/%s", cfg.DefaultUser, cfg.DefaultPass, host, cfg.Port, cfg.DefaultDB))
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
		connection: func(host string, cfg ServiceConfig) Connection {
			return connectionURL(fmt.Sprintf("http://%s:%s@%s:%d/%s", cfg.DefaultUser, cfg.DefaultPass, host, cfg.Port, cfg.DefaultDB))
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
		connection: func(host string, cfg ServiceConfig) Connection {
			return connectionURL(fmt.Sprintf("http://%s:%d", host, cfg.Port))
		},
		credentials: func(cfg ServiceConfig) []Credential {
			return []Credential{{Label: "Master key", Value: cfg.DefaultPass}}
		},
	},
	ServiceSQLServer: {
		defaults: ServiceConfig{Image: "mcr.microsoft.com/mssql/server:2025-latest", Port: 1433, DefaultUser: "devpad", DefaultDB: "devpad"},
		// The image's data, logs and secrets all live under here, owned by
		// the image's non-root mssql user.
		dataDir: "/var/opt/mssql",
		env: func(cfg ServiceConfig) []string {
			return []string{
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
		connection: func(host string, cfg ServiceConfig) Connection {
			// ADO.NET form. SQL Server's certificate is self-signed, so
			// clients that encrypt by default have to be told to trust it.
			return Connection{
				Label: "Connection string",
				Value: fmt.Sprintf("Server=%s,%d;Database=%s;User Id=%s;Password=%s;TrustServerCertificate=True", host, cfg.Port, cfg.DefaultDB, cfg.DefaultUser, cfg.DefaultPass),
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
			acceptEnv: []string{"ACCEPT_EULA=Y"},
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

// catalogLicense returns a copy of the definition's licence terms for the
// catalog, or nil when it has none.
func (d definition) catalogLicense() *License {
	if d.license == nil {
		return nil
	}
	l := *d.license
	l.Notes = slices.Clone(l.Notes)
	l.acceptEnv = nil
	return &l
}

// containerEnv is the sidecar container's environment. For a service with
// licence terms it accepts them for the image, and so it refuses unless cfg
// records a user accepting them.
func (d definition) containerEnv(cfg ServiceConfig) ([]string, error) {
	if d.license == nil {
		return d.env(cfg), nil
	}
	if cfg.LicenseAcceptance == nil {
		return nil, ErrLicenseNotAccepted
	}
	return slices.Concat(d.env(cfg), d.license.acceptEnv), nil
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

// credentialFields are the names and secrets the IDE shows for the service.
func (d definition) credentialFields(cfg ServiceConfig) []Credential {
	if d.credentials != nil {
		return d.credentials(cfg)
	}
	return []Credential{
		{Label: "User", Value: cfg.DefaultUser},
		{Label: "Password", Value: cfg.DefaultPass},
		{Label: "Database", Value: cfg.DefaultDB},
	}
}

func connectionURL(url string) Connection {
	return Connection{Label: "Connection URL", Value: url}
}
