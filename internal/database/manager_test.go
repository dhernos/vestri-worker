package database

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNetworkFromCompose(t *testing.T) {
	tests := []struct {
		name    string
		compose string
		want    string
	}{
		{"implicit default", `{"name":"game","services":{"minecraft":{}}}`, "game_default"},
		{"normalized default", `{"name":"mc","services":{"minecraft":{"networks":{"default":null}}},"networks":{"default":{"name":"game_default"}}}`, "game_default"},
		{"shared velocity", `{"services":{"minecraft":{"networks":{"default":null,"velocity":null}},"helper":{}},"networks":{"default":{"name":"game_default"},"velocity":{"name":"velocity_shared"}}}`, "velocity_shared"},
		{"alternate service", `{"services":{"modpack":{"networks":{"game":null}}},"networks":{"game":{"name":"modpack_game"}}}`, "modpack_game"},
		{"host mode", `{"name":"game","services":{"minecraft":{"network_mode":"host"}}}`, ""},
		{"ambiguous networks", `{"services":{"minecraft":{"networks":{"first":null,"second":null}}},"networks":{"first":{"name":"first"},"second":{"name":"second"}}}`, ""},
		{"multiple services without minecraft", `{"name":"game","services":{"modpack":{},"helper":{}}}`, ""},
		{"unsafe network", `{"services":{"minecraft":{"networks":{"default":null}}},"networks":{"default":{"name":"bad${NETWORK}"}}}`, ""},
		{"unsafe implicit network", `{"name":"../game","services":{"minecraft":{}}}`, ""},
		{"missing network name", `{"services":{"modpack":{"networks":{"game":null}}}}`, ""},
		{"no services", `{"name":"game","services":{}}`, ""},
		{"malformed", `{`, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := networkFromCompose([]byte(test.compose))
			if test.want == "" {
				if err == nil {
					t.Fatalf("networkFromCompose() = %q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("networkFromCompose() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestStateFromCompose(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"running", `{"State":"running"}`, "running"},
		{"healthy", `{"State":"running","Health":"healthy"}`, "running"},
		{"starting health", `{"State":"running","Health":"starting"}`, "starting"},
		{"unhealthy", `{"State":"running","Health":"unhealthy"}`, "unknown"},
		{"exited", `{"State":"exited"}`, "stopped"},
		{"created", `{"State":"created"}`, "starting"},
		{"restarting", `{"State":"restarting"}`, "starting"},
	}
	for _, test := range tests {
		for _, format := range []string{"array", "ndjson"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				output := " \n" + test.output + "\n"
				if format == "array" {
					output = "[" + test.output + "]"
				}
				if got := stateFromCompose([]byte(output)); got != test.want {
					t.Fatalf("stateFromCompose(%q) = %q, want %q", output, got, test.want)
				}
			})
		}
	}
	for _, test := range []struct {
		name   string
		output string
		want   string
	}{
		{"empty", "", "stopped"},
		{"whitespace", " \n\t", "stopped"},
		{"empty array", `[]`, "stopped"},
		{"multiple ndjson records", "{\"State\":\"exited\"}\n{\"State\":\"running\",\"Health\":\"healthy\"}\n", "running"},
		{"multiple array records", `[{"State":"exited"},{"State":"running"}]`, "running"},
		{"malformed array", `[{`, "unknown"},
		{"malformed object", `{`, "unknown"},
		{"malformed ndjson tail", "{\"State\":\"running\"}\ninvalid", "unknown"},
		{"unexpected closing bracket", `]`, "unknown"},
		{"unexpected closing brace", `}`, "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := stateFromCompose([]byte(test.output)); got != test.want {
				t.Fatalf("stateFromCompose(%q) = %q, want %q", test.output, got, test.want)
			}
		})
	}
}

type managerTestCall struct {
	directory string
	args      []string
}

type managerTestRunner struct {
	t              *testing.T
	calls          []managerTestCall
	missingNetwork bool
	failNextUp     bool
}

func (runner *managerTestRunner) run(_ context.Context, directory string, args ...string) ([]byte, error) {
	runner.t.Helper()
	runner.calls = append(runner.calls, managerTestCall{directory, append([]string(nil), args...)})
	if reflect.DeepEqual(args, []string{"network", "inspect", "game_default"}) {
		if runner.missingNetwork {
			return nil, errors.New("network missing")
		}
		return []byte(`[]`), nil
	}
	if len(args) < 4 || args[0] != "compose" || args[1] != "-f" || args[2] != filepath.Join(directory, "docker-compose.yml") {
		runner.t.Errorf("unexpected Docker call in %q: %q", directory, args)
		return nil, errors.New("unexpected Docker arguments")
	}
	switch {
	case reflect.DeepEqual(args[3:], []string{"config", "--format", "json"}):
		return []byte(`{"name":"mc","services":{"minecraft":{"networks":{"default":null}}},"networks":{"default":{"name":"game_default"}}}`), nil
	case reflect.DeepEqual(args[3:], []string{"ps", "--all", "--format", "json"}):
		return []byte(`[{"State":"running","Health":"healthy"}]`), nil
	case reflect.DeepEqual(args[3:], []string{"up", "-d"}):
		if runner.failNextUp {
			runner.failNextUp = false
			return nil, errors.New("initial startup failed")
		}
		return nil, nil
	case reflect.DeepEqual(args[3:], []string{"stop"}), reflect.DeepEqual(args[3:], []string{"down", "--remove-orphans", "--volumes"}):
		return nil, nil
	default:
		runner.t.Errorf("unexpected Compose arguments: %q", args)
		return nil, errors.New("unexpected Compose arguments")
	}
}

func newManagerTest(t *testing.T) (*Manager, *managerTestRunner) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "servers")
	if err := os.MkdirAll(filepath.Join(base, "stack"), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(base, manager.store)
	if err != nil || !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("store %q must be outside game base %q: %v", manager.store, base, err)
	}
	if filepath.Dir(manager.store) != filepath.Dir(base) {
		t.Fatalf("store %q must use the temporary parent of %q", manager.store, base)
	}
	runner := &managerTestRunner{t: t}
	manager.run = runner.run
	return manager, runner
}

func managerTestEntry(t *testing.T, state State) Entry {
	t.Helper()
	if len(state.Databases) != 1 {
		t.Fatalf("got %d databases, want one", len(state.Databases))
	}
	return state.Databases[0]
}

func managerTestMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("permissions of %q = %04o, want %04o", path, got, want)
	}
}

func managerTestHex(t *testing.T, value string, length int) {
	t.Helper()
	if _, err := hex.DecodeString(value); err != nil || len(value) != length {
		t.Errorf("expected %d hexadecimal characters, got %q", length, value)
	}
}

func TestManagerLifecycle(t *testing.T) {
	for _, engine := range []string{"mariadb", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			manager, runner := newManagerTest(t)
			ctx := context.Background()
			state, err := manager.Create(ctx, "owner", "stack", "  Plugin DB  ", engine)
			if err != nil {
				t.Fatal(err)
			}
			entry := managerTestEntry(t, state)
			port := 3306
			if engine == "postgres" {
				port = 5432
			}
			if entry.Name != "Plugin DB" || entry.Engine != engine || entry.Port != port || entry.Host != "db-"+entry.ID || entry.Database != "plugins" || entry.Username != "plugin_user" || entry.State != "running" || entry.Error != "" {
				t.Fatalf("unexpected database entry: %+v", entry)
			}
			managerTestHex(t, entry.ID, 24)
			managerTestHex(t, entry.Password, 48)
			_, scope, err := manager.scope("owner", "stack")
			if err != nil {
				t.Fatal(err)
			}
			databaseDir := filepath.Join(scope, entry.ID)
			for _, directory := range []string{manager.store, scope, databaseDir} {
				managerTestMode(t, directory, 0700)
			}
			for _, name := range []string{"docker-compose.yml", "database.json"} {
				managerTestMode(t, filepath.Join(databaseDir, name), 0600)
			}
			body, err := os.ReadFile(filepath.Join(databaseDir, "database.json"))
			if err != nil {
				t.Fatal(err)
			}
			var stored record
			if err := json.Unmarshal(body, &stored); err != nil {
				t.Fatal(err)
			}
			managerTestHex(t, stored.RootPassword, 48)
			if stored.RootPassword == entry.Password || stored.Network != "game_default" || stored.Entry.ID != entry.ID || stored.Entry.Password != entry.Password {
				t.Fatal("metadata must retain the database credentials and a distinct administrator password")
			}
			body, err = json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "rootPassword") || strings.Contains(string(body), stored.RootPassword) {
				t.Fatal("public State exposes administrator credentials")
			}
			if err := filepath.WalkDir(manager.base, func(path string, item os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !item.IsDir() {
					t.Errorf("database file leaked inside game root: %s", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 4 || runner.calls[0].directory != filepath.Join(manager.base, "stack") || runner.calls[2].directory != databaseDir || !reflect.DeepEqual(runner.calls[2].args, composeArgs(databaseDir, "up", "-d")) {
				t.Fatalf("unexpected create calls: %+v", runner.calls)
			}
			runner.calls = nil
			if _, err := manager.Create(ctx, "owner", "stack", "plugin db", engine); err == nil {
				t.Fatal("case-insensitive duplicate name accepted")
			}
			if len(runner.calls) != 0 {
				t.Fatal("duplicate creation invoked Docker")
			}
			sentinel := filepath.Join(databaseDir, "data", "sentinel")
			if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sentinel, []byte("persistent database data"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Action(ctx, "owner", "stack", entry.ID, "stop", ""); err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[0].args, composeArgs(databaseDir, "stop")) {
				t.Fatalf("stop must use compose stop, got %+v", runner.calls)
			}
			if body, err := os.ReadFile(sentinel); err != nil || string(body) != "persistent database data" {
				t.Fatalf("stop lost persistent data: %q, %v", body, err)
			}
			runner.calls = nil
			if _, err := manager.Action(ctx, "owner", "stack", entry.ID, "delete", "plugin db"); err == nil {
				t.Fatal("delete accepted a non-exact confirmation")
			}
			if len(runner.calls) != 0 {
				t.Fatal("incorrect delete confirmation invoked Docker")
			}
			state, err = manager.Action(ctx, "owner", "stack", entry.ID, "delete", entry.Name)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Databases) != 0 || len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, composeArgs(databaseDir, "down", "--remove-orphans", "--volumes")) {
				t.Fatalf("unexpected deletion: state=%+v calls=%+v", state, runner.calls)
			}
			if _, err := os.Stat(databaseDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deleted database directory remains: %v", err)
			}
		})
	}
}

func TestManagerFailedCreateCanRetry(t *testing.T) {
	manager, runner := newManagerTest(t)
	ctx := context.Background()
	runner.failNextUp = true
	if _, err := manager.Create(ctx, "owner", "stack", "Retry DB", "postgres"); err == nil {
		t.Fatal("initial up failure must be reported")
	}
	state, err := manager.List(ctx, "owner", "stack")
	if err != nil {
		t.Fatal(err)
	}
	entry := managerTestEntry(t, state)
	_, scope, err := manager.scope("owner", "stack")
	if err != nil {
		t.Fatal(err)
	}
	databaseDir := filepath.Join(scope, entry.ID)
	metadata, err := os.ReadFile(filepath.Join(databaseDir, "database.json"))
	if err != nil {
		t.Fatal(err)
	}
	managerTestMode(t, filepath.Join(databaseDir, "docker-compose.yml"), 0600)
	runner.calls = nil
	state, err = manager.Action(ctx, "owner", "stack", entry.ID, "start", "")
	if err != nil {
		t.Fatal(err)
	}
	retried := managerTestEntry(t, state)
	if retried.ID != entry.ID || retried.Password != entry.Password || retried.State != "running" {
		t.Fatalf("retry changed identity or credentials: %+v", retried)
	}
	if len(runner.calls) != 4 || !reflect.DeepEqual(runner.calls[2].args, composeArgs(databaseDir, "up", "-d")) {
		t.Fatalf("unexpected retry calls: %+v", runner.calls)
	}
	after, err := os.ReadFile(filepath.Join(databaseDir, "database.json"))
	if err != nil || string(after) != string(metadata) {
		t.Fatalf("retry changed saved metadata: %v", err)
	}
}

func TestManagerOwnerScope(t *testing.T) {
	manager, runner := newManagerTest(t)
	ctx := context.Background()
	state, err := manager.Create(ctx, "owner", "stack", "Private DB", "mariadb")
	if err != nil {
		t.Fatal(err)
	}
	entry := managerTestEntry(t, state)
	runner.calls = nil
	for _, action := range []string{"start", "stop", "delete"} {
		if _, err := manager.Action(ctx, "other-owner", "stack", entry.ID, action, entry.Name); err == nil {
			t.Errorf("another owner could %s database", action)
		}
	}
	state, err = manager.List(ctx, "other-owner", "stack")
	if err != nil || len(state.Databases) != 0 {
		t.Fatalf("another owner can see databases: %+v, %v", state, err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("cross-owner requests invoked Docker")
	}
}

func TestManagerMissingNetworkDoesNotStoreDatabase(t *testing.T) {
	manager, runner := newManagerTest(t)
	runner.missingNetwork = true
	if _, err := manager.Create(context.Background(), "owner", "stack", "Unavailable DB", "mariadb"); err == nil {
		t.Fatal("creation succeeded without the game network")
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected only config and network inspect calls, got %+v", runner.calls)
	}
	if _, err := os.Stat(manager.store); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing network created private database storage: %v", err)
	}
	state, err := manager.List(context.Background(), "owner", "stack")
	if err != nil || len(state.Databases) != 0 {
		t.Fatalf("missing network retained a database: %+v, %v", state, err)
	}
}

func TestManagerRejectsInvalidReferences(t *testing.T) {
	manager, runner := newManagerTest(t)
	ctx := context.Background()
	if _, err := New(string(filepath.Separator)); err == nil {
		t.Error("New accepted filesystem root as server base")
	}
	for _, id := range []string{"", "../database", strings.Repeat("a", 23), strings.Repeat("a", 25), strings.Repeat("A", 24), strings.Repeat("g", 24)} {
		if _, err := manager.Action(ctx, "owner", "stack", id, "stop", ""); err == nil {
			t.Errorf("invalid database ID %q accepted", id)
		}
	}
	if _, err := manager.Action(ctx, "owner", "stack", strings.Repeat("a", 24), "restart", ""); err == nil {
		t.Error("invalid action accepted")
	}
	if _, err := manager.Action(ctx, "owner", "stack", strings.Repeat("a", 24), "stop", ""); err == nil {
		t.Error("unknown database ID accepted")
	}
	for _, reference := range []struct{ owner, stack string }{
		{"", "stack"}, {"../owner", "stack"}, {"owner/name", "stack"},
		{"owner", ""}, {"owner", ".."}, {"owner", "../stack"},
		{"owner", "/tmp"}, {"owner", "missing"},
	} {
		if _, err := manager.List(ctx, reference.owner, reference.stack); err == nil {
			t.Errorf("invalid reference %+v accepted", reference)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(manager.base, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(ctx, "owner", "linked", "Escaped DB", "mariadb"); err == nil {
		t.Error("symlink server path accepted")
	}
	for _, name := range []string{"", "   ", strings.Repeat("a", 49), "bad\nname", "bad\rname", "bad\x00name"} {
		if _, err := manager.Create(ctx, "owner", "stack", name, "mariadb"); err == nil {
			t.Errorf("invalid database name %q accepted", name)
		}
	}
	if _, err := manager.Create(ctx, "owner", "stack", "Unsupported DB", "sqlite"); err == nil {
		t.Error("unsupported database engine accepted")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid requests invoked Docker: %+v", runner.calls)
	}
}
