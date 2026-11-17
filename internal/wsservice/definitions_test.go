package wsservice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestSidecarVolumesMountWhereEachImageKeepsItsData(t *testing.T) {
	want := map[ServiceType]string{
		ServicePostgres:    "/data",
		ServiceMongoDB:     "/data/db",
		ServiceMariaDB:     "/var/lib/mysql",
		ServiceCouchDB:     "/opt/couchdb/data",
		ServiceMeilisearch: "/data",
		ServiceSQLServer:   "/var/opt/mssql",
	}
	for serviceType, def := range definitions {
		dir, ok := want[serviceType]
		if !ok {
			t.Errorf("%s: no data directory listed in this test", serviceType)
			continue
		}
		if def.dataDir != dir {
			t.Errorf("%s: expected data directory %s, got %s", serviceType, dir, def.dataDir)
		}
	}
}

func TestEveryServiceDefinitionIsComplete(t *testing.T) {
	cfg := ServiceConfig{DefaultUser: "u", DefaultPass: "p", DefaultDB: "d", LicenseAcceptance: &LicenseAcceptance{UserID: 1}}
	for serviceType, def := range definitions {
		if def.defaults.Image == "" || def.defaults.Port == 0 {
			t.Errorf("%s: no image or port", serviceType)
		}
		if def.dataDir == "" {
			t.Errorf("%s: no data directory", serviceType)
		}
		if def.env == nil || len(def.env(cfg)) == 0 {
			t.Errorf("%s: no container environment", serviceType)
		}
		if len(def.connectionVars(serviceType, "host", cfg)) == 0 {
			t.Errorf("%s: no connection environment", serviceType)
		}
		if def.connection == nil {
			t.Errorf("%s: no connection", serviceType)
		} else if c := def.connection("host", cfg); c.Label == "" || c.Value == "" {
			t.Errorf("%s: connection needs a label and a value, got %+v", serviceType, c)
		}
		if len(def.credentialFields(cfg)) == 0 {
			t.Errorf("%s: no credentials", serviceType)
		}
		l := def.license
		if l == nil {
			continue
		}
		if l.Name == "" || !strings.HasPrefix(l.URL, "https://") {
			t.Errorf("%s: licence needs a name and an https URL, got %+v", serviceType, *l)
		}
		// Terms devpad accepts for the user are accepted in acceptEnv, and
		// only there, so that no container accepts them unasked.
		if len(l.acceptEnv) == 0 {
			t.Errorf("%s: licence does not say what accepts it", serviceType)
		}
		for _, v := range def.env(cfg) {
			if slices.Contains(l.acceptEnv, v) {
				t.Errorf("%s: env sets %s, which only the licence's acceptEnv may set", serviceType, v)
			}
		}
	}
}

func TestCatalogListsEveryTypeWithItsLicenceTerms(t *testing.T) {
	svc := NewService(&fakeRepository{}, &fakeContainerManager{})
	catalog := svc.Catalog(context.Background())

	var types []string
	var licensed []ServiceType
	for _, entry := range catalog {
		types = append(types, string(entry.Type))
		if entry.License != nil {
			licensed = append(licensed, entry.Type)
		}
	}
	if !slices.Equal(types, SupportedTypes()) {
		t.Fatalf("expected the catalog to list %v, got %v", SupportedTypes(), types)
	}
	if !slices.Equal(licensed, []ServiceType{ServiceSQLServer}) {
		t.Fatalf("expected only sqlserver to have licence terms, got %v", licensed)
	}

	sqlserver := catalogEntry(t, catalog, ServiceSQLServer)
	if len(sqlserver.License.Notes) == 0 {
		t.Fatalf("expected sqlserver's terms with notes, got %+v", sqlserver.License)
	}
	if sqlserver.License.acceptEnv != nil {
		t.Fatal("the catalog's terms carry the env that accepts them")
	}
	body, err := json.Marshal(sqlserver)
	if err != nil {
		t.Fatalf("encoding catalog entry: %v", err)
	}
	if strings.Contains(string(body), "ACCEPT_EULA") {
		t.Fatalf("catalog entry sent the env that accepts the terms: %s", body)
	}

	// The catalog gets a copy: changing it must not change the terms.
	sqlserver.License.Notes[0] = "changed"
	if catalogEntry(t, svc.Catalog(context.Background()), ServiceSQLServer).License.Notes[0] == "changed" {
		t.Fatal("Catalog returned the definition's own notes")
	}
	if !slices.Contains(definitions[ServiceSQLServer].license.acceptEnv, "ACCEPT_EULA=Y") {
		t.Fatal("Catalog changed the definition's acceptEnv")
	}
}

func TestDeleteRemovesTheSidecarWithItsAnonymousVolumes(t *testing.T) {
	repo := &fakeRepository{
		services: map[int64]*WorkspaceService{
			1: {ID: 1, WorkspaceID: 42, ServiceType: ServiceMariaDB, ContainerID: "mariadb-container", VolumeName: "devpad-svc-42-mariadb", Config: "{}"},
		},
	}
	manager := &fakeContainerManager{}

	if err := NewService(repo, manager).Delete(context.Background(), 1); err != nil {
		t.Fatalf("deleting service: %v", err)
	}
	if !slices.Equal(manager.removedSidecar, []string{"mariadb-container"}) || len(manager.removed) != 0 {
		t.Fatalf("expected the container removed as a sidecar, got sidecar %v and plain %v", manager.removedSidecar, manager.removed)
	}
}

func unavailableTypes(catalog []CatalogEntry) []ServiceType {
	var types []ServiceType
	for _, entry := range catalog {
		if entry.Unavailable != "" {
			types = append(types, entry.Type)
		}
	}
	return types
}

func catalogEntry(t *testing.T, catalog []CatalogEntry, serviceType ServiceType) CatalogEntry {
	t.Helper()
	for _, entry := range catalog {
		if entry.Type == serviceType {
			return entry
		}
	}
	t.Fatalf("catalog has no %s", serviceType)
	return CatalogEntry{}
}

func TestGeneratePasswordUsesThreeCharacterClasses(t *testing.T) {
	for range 2000 {
		p := generatePassword(24)
		if len(p) != 24 {
			t.Fatalf("expected 24 characters, got %q", p)
		}
		if !strings.ContainsAny(p, "abcdefghijklmnopqrstuvwxyz") || !strings.ContainsAny(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(p, "0123456789") {
			t.Fatalf("password %q lacks a character class", p)
		}
	}
}

func TestServiceResponseShowsHowToConnect(t *testing.T) {
	cfg := `{"image":"img","port":%d,"defaultUser":"devpad","defaultPass":"Pass1","defaultDb":"app"}`
	tests := []struct {
		serviceType ServiceType
		port        int
		connection  Connection
		credentials []Credential
	}{
		{ServicePostgres, 5432, Connection{"Connection URL", "postgresql://devpad:Pass1@devpad-svc-7-postgres:5432/app"}, nil},
		{ServiceMongoDB, 27017, Connection{"Connection URL", "mongodb://devpad:Pass1@devpad-svc-7-mongodb:27017/app?authSource=admin"}, nil},
		{ServiceMariaDB, 3306, Connection{"Connection URL", "mysql://devpad:Pass1@devpad-svc-7-mariadb:3306/app"}, nil},
		{ServiceCouchDB, 5984, Connection{"Connection URL", "http://devpad:Pass1@devpad-svc-7-couchdb:5984/app"}, nil},
		{ServiceMeilisearch, 7700, Connection{"Connection URL", "http://devpad-svc-7-meilisearch:7700"}, []Credential{{"Master key", "Pass1"}}},
		{ServiceSQLServer, 1433, Connection{"Connection string", "Server=devpad-svc-7-sqlserver,1433;Database=app;User Id=devpad;Password=Pass1;TrustServerCertificate=True"}, nil},
	}
	defaultCredentials := []Credential{{"User", "devpad"}, {"Password", "Pass1"}, {"Database", "app"}}
	for _, tt := range tests {
		t.Run(string(tt.serviceType), func(t *testing.T) {
			resp := serviceResponse(&WorkspaceService{WorkspaceID: 7, ServiceType: tt.serviceType, Config: fmt.Sprintf(cfg, tt.port)})
			if host := resp["host"]; host != "devpad-svc-7-"+string(tt.serviceType) {
				t.Errorf("unexpected host %v", host)
			}
			if c := resp["connection"]; c != tt.connection {
				t.Errorf("unexpected connection:\nwant: %+v\n got: %+v", tt.connection, c)
			}
			want := tt.credentials
			if want == nil {
				want = defaultCredentials
			}
			if got := resp["credentials"]; !reflect.DeepEqual(got, want) {
				t.Errorf("unexpected credentials:\nwant: %+v\n got: %+v", want, got)
			}
		})
	}

	// A row of a type devpad no longer defines still has a host to show.
	resp := serviceResponse(&WorkspaceService{WorkspaceID: 7, ServiceType: "oracle", Config: "{}"})
	if resp["host"] != "devpad-svc-7-oracle" || resp["connection"] != (Connection{}) || !reflect.DeepEqual(resp["credentials"], []Credential{}) {
		t.Errorf("unexpected response for an unknown type: %+v", resp)
	}
}

// The frontend's catalog gives each service type its label, icon and
// description, and lists them in catalog order.
func TestServiceCatalogMatchesFrontend(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "components", "ide", "serviceCatalog.ts"))
	if err != nil {
		t.Fatalf("reading the frontend's service catalog: %v", err)
	}
	block := regexp.MustCompile(`(?s)export const serviceCatalog: ServiceCatalogItem\[\] = \[(.*?)\n\]`).FindSubmatch(source)
	if block == nil {
		t.Fatal("cannot find serviceCatalog in frontend/src/components/ide/serviceCatalog.ts")
	}
	var frontend []string
	for _, m := range regexp.MustCompile(`\bvalue: '([\w-]+)'`).FindAllSubmatch(block[1], -1) {
		frontend = append(frontend, string(m[1]))
	}
	sort.Strings(frontend)
	if !slices.Equal(frontend, SupportedTypes()) {
		t.Errorf("wsservice defines %v, but the frontend's serviceCatalog lists %v", SupportedTypes(), frontend)
	}
}
