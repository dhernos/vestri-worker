package database

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseDockerIntegration(t *testing.T) {
	if os.Getenv("VESTRI_DATABASE_DOCKER_TESTS") != "1" {
		t.Skip("set VESTRI_DATABASE_DOCKER_TESTS=1 to run isolated Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	root := t.TempDir()
	base := filepath.Join(root, "servers")
	serverDir := filepath.Join(base, "test-server")
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		t.Fatal(err)
	}
	token, err := randomToken(8)
	if err != nil {
		t.Fatal(err)
	}
	network := "vestri-db-test-" + token
	if output, err := runDocker(ctx, root, "network", "create", network); err != nil {
		t.Fatalf("network: %v: %s", err, output)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = runDocker(cleanupCtx, root, "network", "rm", network)
	})
	serverCompose := fmt.Sprintf("name: vestri-test-%s\nservices:\n  minecraft:\n    image: alpine:3.21\n    networks: [server]\nnetworks:\n  server:\n    external: true\n    name: %s\n", token, network)
	if err := os.WriteFile(filepath.Join(serverDir, "docker-compose.yml"), []byte(serverCompose), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"mariadb", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			state, createErr := manager.Create(ctx, "test-owner", "test-server", "integration-"+engine, engine)
			_, ownerDir, _ := manager.scope("test-owner", "test-server")
			records, readErr := readRecords(ownerDir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, stored := range records {
				databaseDir := filepath.Join(ownerDir, stored.Entry.ID)
				t.Cleanup(func() {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
					defer cleanupCancel()
					_, _ = runDocker(cleanupCtx, databaseDir, composeArgs(databaseDir, "down", "--remove-orphans", "--volumes")...)
				})
			}
			if createErr != nil {
				t.Fatal(createErr)
			}
			if len(state.Databases) != 1 {
				t.Fatalf("expected one database, got %d", len(state.Databases))
			}
			entry := state.Databases[0]
			databaseDir := filepath.Join(ownerDir, entry.ID)
			if output, err := runDocker(ctx, databaseDir, composeArgs(databaseDir, "up", "-d", "--wait", "--wait-timeout", "120")...); err != nil {
				t.Fatalf("database readiness: %v: %s", err, output)
			}
			containerID, err := runDocker(ctx, databaseDir, composeArgs(databaseDir, "ps", "-q", "database")...)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := runDocker(ctx, databaseDir, "inspect", "--format", "{{json .HostConfig.PortBindings}}", strings.TrimSpace(string(containerID)))
			if err != nil {
				t.Fatal(err)
			}
			var bindings map[string]interface{}
			if json.Unmarshal(inspection, &bindings) != nil || len(bindings) != 0 {
				t.Fatalf("database publishes host ports: %s", inspection)
			}
			var output []byte
			if engine == "mariadb" {
				output, err = runDocker(ctx, databaseDir, "run", "--rm", "--network", network, "--env", "MYSQL_PWD="+entry.Password, "mariadb:11.4", "mariadb", "--host", entry.Host, "--user", entry.Username, "--database", entry.Database, "--execute", "CREATE TABLE integration_test (id INT); INSERT INTO integration_test VALUES (42); SELECT id FROM integration_test;")
			} else {
				output, err = runDocker(ctx, databaseDir, "run", "--rm", "--network", network, "--env", "PGPASSWORD="+entry.Password, "postgres:17-alpine", "psql", "--host", entry.Host, "--username", entry.Username, "--dbname", entry.Database, "--tuples-only", "--command", "CREATE TABLE integration_test (id INT); INSERT INTO integration_test VALUES (42); SELECT id FROM integration_test; SELECT rolsuper FROM pg_roles WHERE rolname = current_user;")
			}
			if err != nil || !strings.Contains(string(output), "42") {
				t.Fatalf("plugin-user login/schema access failed: %v: %s", err, output)
			}
			if engine == "postgres" && !strings.Contains(string(output), "f") {
				t.Fatalf("plugin PostgreSQL user must not be a superuser: %s", output)
			}
			if _, err := manager.Action(ctx, "test-owner", "test-server", entry.ID, "stop", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Action(ctx, "test-owner", "test-server", entry.ID, "start", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Action(ctx, "test-owner", "test-server", entry.ID, "delete", entry.Name); err != nil {
				t.Fatal(err)
			}
		})
	}
}
