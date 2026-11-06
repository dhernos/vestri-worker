package database

import (
	"strings"
	"testing"
)

func TestDatabaseComposePrivateNetwork(t *testing.T) {
	for _, engine := range []string{"mariadb", "postgres"} {
		values := composeValues{Project: "vestri-db-abc", Database: "plugins", Username: "plugin_user", Password: "abc123", RootPassword: "root123", Host: "db-abc", Network: "vestri-velocity-test"}
		compose, err := renderCompose(engine, values)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(compose, "ports:") || strings.Contains(compose, "network_mode:") {
			t.Fatalf("database must not publish host ports: %s", compose)
		}
		if !strings.Contains(compose, "external: true") || !strings.Contains(compose, `name: "vestri-velocity-test"`) || !strings.Contains(compose, `- "db-abc"`) {
			t.Fatalf("database must use the existing server network and its unique alias: %s", compose)
		}
		values.Network = "bad\nports: [5432:5432]"
		if _, err := renderCompose(engine, values); err == nil {
			t.Fatal("accepted unsafe network name")
		}
	}
}
