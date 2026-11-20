package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestServiceEnvOverridesTheContainersAndOutlastsARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".devpad", "service-env.json")
	store := newServiceEnvStore(path)
	if err := store.set([]string{"DATABASE_URL=postgres://new", "PGHOST=svc"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	base := []string{"PATH=/bin", "DATABASE_URL=postgres://old"}
	want := []string{"PATH=/bin", "DATABASE_URL=postgres://new", "PGHOST=svc", "TERM=xterm"}
	if got := store.environ(base, "TERM=xterm"); !reflect.DeepEqual(got, want) {
		t.Fatalf("environ = %v, want %v", got, want)
	}

	// The agent that starts with the container next reads them back.
	restarted := newServiceEnvStore(path)
	if got := restarted.environ(base, "TERM=xterm"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a restart, environ = %v, want %v", got, want)
	}

	// Setting none takes them all away again.
	if err := restarted.set(nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := newServiceEnvStore(path).environ(base); !reflect.DeepEqual(got, base) {
		t.Fatalf("after clearing, environ = %v, want %v", got, base)
	}
}

func TestServiceEnvDropsTheVariablesOfRemovedServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".devpad", "service-env.json")
	// The container was made with Postgres and MongoDB.
	base := []string{"PATH=/bin", "POSTGRES_HOST=pg", "MONGODB_HOST=mongo", "DEVPAD_SERVICE_VARS=POSTGRES_HOST,MONGODB_HOST"}

	// Until devpad sends the variables, the container's are the ones.
	store := newServiceEnvStore(path)
	if got, want := store.environ(base), []string{"PATH=/bin", "POSTGRES_HOST=pg", "MONGODB_HOST=mongo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before a sync, environ = %v, want %v", got, want)
	}

	// MongoDB was removed and MariaDB added.
	if err := store.set([]string{"POSTGRES_HOST=pg", "MARIADB_HOST=maria"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	want := []string{"PATH=/bin", "POSTGRES_HOST=pg", "MARIADB_HOST=maria"}
	if got := store.environ(base); !reflect.DeepEqual(got, want) {
		t.Fatalf("environ = %v, want %v", got, want)
	}
	if got := newServiceEnvStore(path).environ(base); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a restart, environ = %v, want %v", got, want)
	}

	// Removing every service leaves none.
	if err := store.set(nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := newServiceEnvStore(path).environ(base); !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Fatalf("with no services, environ = %v", got)
	}
}

func TestServiceEnvRefusesBadVariables(t *testing.T) {
	store := newServiceEnvStore(filepath.Join(t.TempDir(), "env.json"))
	for _, env := range [][]string{
		{"NOEQUALS"},
		{"=value"},
		{"1ABC=x"},
		{"BAD NAME=x"},
		{"A=x\x00y"},
	} {
		if err := store.set(env); err == nil {
			t.Errorf("set(%q) succeeded", env)
		}
	}
	if got := store.environ([]string{"A=1"}); !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Fatalf("a refused set changed the environment: %v", got)
	}
}

func TestHandleSetServiceEnv(t *testing.T) {
	old := serviceEnv
	serviceEnv = newServiceEnvStore(filepath.Join(t.TempDir(), "env.json"))
	t.Cleanup(func() { serviceEnv = old })

	put := func(body string) int {
		rec := httptest.NewRecorder()
		handleSetServiceEnv(rec, httptest.NewRequest(http.MethodPut, "/api/env/services", strings.NewReader(body)))
		return rec.Code
	}
	if code := put(`{"env":["REDIS_URL=redis://svc:6379"]}`); code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	if got := processEnv(); !contains(got, "REDIS_URL=redis://svc:6379") {
		t.Fatalf("processEnv lacks the service's variable: %v", got)
	}
	if code := put(`{"env":["bad name=x"]}`); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if code := put(`not json`); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
