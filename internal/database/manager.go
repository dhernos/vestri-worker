package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"vestri-worker/internal/http/fs"
	"vestri-worker/internal/http/stack"
)

type Entry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

type record struct {
	Entry        Entry  `json:"entry"`
	Network      string `json:"network"`
	RootPassword string `json:"rootPassword,omitempty"`
}

type Template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

type State struct {
	Databases []Entry    `json:"databases"`
	Templates []Template `json:"templates"`
}

type dockerRunner func(context.Context, string, ...string) ([]byte, error)

type Manager struct {
	base  string
	store string
	run   dockerRunner
}

var stackNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var databaseIDPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

func New(base string) (*Manager, error) {
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	if abs == string(filepath.Separator) {
		return nil, fmt.Errorf("a dedicated server base directory is required")
	}
	digest := sha256.Sum256([]byte(abs))
	store := filepath.Join(filepath.Dir(abs), ".vestri-databases-"+hex.EncodeToString(digest[:6]))
	return &Manager{base: abs, store: store, run: runDocker}, nil
}

func runDocker(ctx context.Context, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = directory
	return command.CombinedOutput()
}

func (manager *Manager) scope(owner, stackName string) (string, string, error) {
	if !stackNamePattern.MatchString(owner) || !stackNamePattern.MatchString(stackName) {
		return "", "", fmt.Errorf("invalid server reference")
	}
	serverDir, err := fs.SafeSubPath(manager.base, stackName)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(serverDir)
	if err != nil {
		return "", "", fmt.Errorf("server directory is unavailable")
	}
	if resolved != serverDir {
		return "", "", fmt.Errorf("server directory must not contain symlinks")
	}
	ownerHash := sha256.Sum256([]byte(owner + ":" + stackName))
	return serverDir, filepath.Join(manager.store, hex.EncodeToString(ownerHash[:16])), nil
}

func composeArgs(directory string, args ...string) []string {
	return append([]string{"compose", "-f", filepath.Join(directory, "docker-compose.yml")}, args...)
}

func (manager *Manager) network(ctx context.Context, directory string) (string, error) {
	output, err := manager.run(ctx, directory, composeArgs(directory, "config", "--format", "json")...)
	if err != nil {
		return "", fmt.Errorf("could not read server Compose configuration")
	}
	network, err := networkFromCompose(output)
	if err != nil {
		return "", err
	}
	if _, err := manager.run(ctx, directory, "network", "inspect", network); err != nil {
		return "", fmt.Errorf("server Docker network does not exist; start the Minecraft or Velocity server first")
	}
	return network, nil
}

func networkFromCompose(output []byte) (string, error) {
	var config struct {
		Name     string `json:"name"`
		Services map[string]struct {
			NetworkMode string                     `json:"network_mode"`
			Networks    map[string]json.RawMessage `json:"networks"`
		} `json:"services"`
		Networks map[string]struct {
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(output, &config); err != nil {
		return "", fmt.Errorf("invalid server Compose configuration")
	}
	if len(config.Services) == 0 {
		return "", fmt.Errorf("server Compose contains no services")
	}
	service, found := config.Services["minecraft"]
	if !found {
		if len(config.Services) != 1 {
			return "", fmt.Errorf("cannot determine game service network in a multi-service Compose stack")
		}
		for _, candidate := range config.Services {
			service = candidate
		}
	}
	if service.NetworkMode != "" {
		return "", fmt.Errorf("database support requires a Compose bridge network, not network_mode")
	}
	key := ""
	if _, exists := service.Networks["velocity"]; exists {
		key = "velocity"
	} else if _, exists := service.Networks["default"]; exists {
		key = "default"
	} else if len(service.Networks) == 1 {
		for candidate := range service.Networks {
			key = candidate
		}
	} else if len(service.Networks) == 0 {
		key = "default"
	}
	if key == "" {
		return "", fmt.Errorf("cannot determine a unique game server Docker network")
	}
	network := config.Networks[key].Name
	if network == "" && key == "default" && config.Name != "" {
		network = config.Name + "_default"
	}
	if !safeComposeValue.MatchString(network) {
		return "", fmt.Errorf("unsupported game server Docker network name")
	}
	return network, nil
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func readRecords(directory string) ([]record, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []record{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]record, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !databaseIDPattern.MatchString(entry.Name()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(directory, entry.Name(), "database.json"))
		if err != nil {
			return nil, fmt.Errorf("could not read database metadata")
		}
		var value record
		if err := json.Unmarshal(body, &value); err != nil || value.Entry.ID != entry.Name() {
			return nil, fmt.Errorf("invalid database metadata")
		}
		records = append(records, value)
	}
	sort.Slice(records, func(first, second int) bool { return records[first].Entry.Name < records[second].Entry.Name })
	return records, nil
}

func (manager *Manager) List(ctx context.Context, owner, stackName string) (State, error) {
	serverDir, directory, err := manager.scope(owner, stackName)
	if err != nil {
		return State{}, err
	}
	defer stack.LockStack(serverDir)()
	return manager.list(ctx, directory)
}

func (manager *Manager) list(ctx context.Context, directory string) (State, error) {
	state := State{Databases: []Entry{}, Templates: []Template{{ID: "mariadb", Name: "MariaDB 11.4 (MySQL)", Port: 3306}, {ID: "postgres", Name: "PostgreSQL 17", Port: 5432}}}
	records, err := readRecords(directory)
	if err != nil {
		return state, err
	}
	for _, stored := range records {
		entry := stored.Entry
		entry.State, entry.Error = "unknown", ""
		databaseDir := filepath.Join(directory, entry.ID)
		output, err := manager.run(ctx, databaseDir, composeArgs(databaseDir, "ps", "--all", "--format", "json")...)
		if err != nil {
			entry.Error = "Could not read database container status"
		} else {
			entry.State = stateFromCompose(output)
		}
		state.Databases = append(state.Databases, entry)
	}
	return state, nil
}

func stateFromCompose(output []byte) string {
	var containers []struct {
		State  string `json:"State"`
		Health string `json:"Health"`
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "stopped"
	}
	if strings.HasPrefix(trimmed, "[") {
		if json.Unmarshal(output, &containers) != nil {
			return "unknown"
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		for {
			var container struct {
				State  string `json:"State"`
				Health string `json:"Health"`
			}
			decodeErr := decoder.Decode(&container)
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				return "unknown"
			}
			containers = append(containers, container)
		}
	}
	if len(containers) == 0 {
		return "stopped"
	}
	for _, container := range containers {
		if container.State == "running" {
			if container.Health == "starting" {
				return "starting"
			}
			if container.Health == "unhealthy" {
				return "unknown"
			}
			return "running"
		}
		if container.State == "restarting" || container.State == "created" {
			return "starting"
		}
	}
	return "stopped"
}

func (manager *Manager) Create(ctx context.Context, owner, stackName, name, engine string) (State, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 48 || strings.ContainsAny(name, "\n\r\x00") {
		return State{}, fmt.Errorf("database name must contain between 1 and 48 characters")
	}
	if engine != "mariadb" && engine != "postgres" {
		return State{}, fmt.Errorf("unsupported database engine")
	}
	serverDir, directory, err := manager.scope(owner, stackName)
	if err != nil {
		return State{}, err
	}
	defer stack.LockStack(serverDir)()
	records, err := readRecords(directory)
	if err != nil {
		return State{}, err
	}
	if len(records) >= 20 {
		return State{}, fmt.Errorf("maximum of 20 database containers per server reached")
	}
	for _, value := range records {
		if strings.EqualFold(value.Entry.Name, name) {
			return State{}, fmt.Errorf("a database with this name already exists")
		}
	}
	network, err := manager.network(ctx, serverDir)
	if err != nil {
		return State{}, err
	}
	id, err := randomToken(12)
	if err != nil {
		return State{}, fmt.Errorf("could not generate database identifier")
	}
	password, err := randomToken(24)
	if err != nil {
		return State{}, fmt.Errorf("could not generate database password")
	}
	rootPassword, err := randomToken(24)
	if err != nil {
		return State{}, fmt.Errorf("could not generate database administrator password")
	}
	port := 3306
	if engine == "postgres" {
		port = 5432
	}
	stored := record{Entry: Entry{ID: id, Name: name, Engine: engine, Host: "db-" + id, Port: port, Database: "plugins", Username: "plugin_user", Password: password, State: "starting"}, Network: network, RootPassword: rootPassword}
	compose, err := renderCompose(engine, composeValues{Project: "vestri-db-" + id, Database: stored.Entry.Database, Username: stored.Entry.Username, Password: password, RootPassword: rootPassword, Host: stored.Entry.Host, Network: network})
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(manager.store, 0700); err != nil {
		return State{}, fmt.Errorf("could not create private database storage")
	}
	if err := os.Chmod(manager.store, 0700); err != nil {
		return State{}, fmt.Errorf("could not secure private database storage")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return State{}, fmt.Errorf("could not create database directory")
	}
	databaseDir := filepath.Join(directory, id)
	if err := os.Mkdir(databaseDir, 0700); err != nil {
		return State{}, fmt.Errorf("could not reserve database directory")
	}
	metadata, err := json.Marshal(stored)
	if err != nil {
		return State{}, err
	}
	if err := os.WriteFile(filepath.Join(databaseDir, "database.json"), metadata, 0600); err != nil {
		os.RemoveAll(databaseDir)
		return State{}, fmt.Errorf("could not save database metadata")
	}
	if err := os.WriteFile(filepath.Join(databaseDir, "docker-compose.yml"), []byte(compose), 0600); err != nil {
		os.RemoveAll(databaseDir)
		return State{}, fmt.Errorf("could not save database Compose template")
	}
	if engine == "postgres" {
		initialization := fmt.Sprintf("CREATE ROLE plugin_user WITH LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE;\nALTER DATABASE plugins OWNER TO plugin_user;\n\\connect plugins\nALTER SCHEMA public OWNER TO plugin_user;\n", password)
		if err := os.WriteFile(filepath.Join(databaseDir, "init.sql"), []byte(initialization), 0644); err != nil {
			os.RemoveAll(databaseDir)
			return State{}, fmt.Errorf("could not save PostgreSQL initialization")
		}
	}
	if _, err := manager.run(ctx, databaseDir, composeArgs(databaseDir, "up", "-d")...); err != nil {
		return State{}, fmt.Errorf("database was saved but could not be started; refresh and use Start to retry")
	}
	return manager.list(ctx, directory)
}

func (manager *Manager) Action(ctx context.Context, owner, stackName, id, action, confirm string) (State, error) {
	if !databaseIDPattern.MatchString(id) {
		return State{}, fmt.Errorf("invalid database identifier")
	}
	if action != "start" && action != "stop" && action != "delete" {
		return State{}, fmt.Errorf("invalid database action")
	}
	serverDir, directory, err := manager.scope(owner, stackName)
	if err != nil {
		return State{}, err
	}
	defer stack.LockStack(serverDir)()
	records, err := readRecords(directory)
	if err != nil {
		return State{}, err
	}
	var selected *record
	for index := range records {
		if records[index].Entry.ID == id {
			selected = &records[index]
			break
		}
	}
	if selected == nil {
		return State{}, fmt.Errorf("database not found")
	}
	if action == "delete" && confirm != selected.Entry.Name {
		return State{}, fmt.Errorf("type the database name to confirm permanent deletion")
	}
	databaseDir := filepath.Join(directory, id)
	args := []string{"stop"}
	if action == "start" {
		network, err := manager.network(ctx, serverDir)
		if err != nil {
			return State{}, err
		}
		if network != selected.Network {
			return State{}, fmt.Errorf("server network changed; existing database network must be restored before starting")
		}
		args = []string{"up", "-d"}
	} else if action == "delete" {
		args = []string{"down", "--remove-orphans", "--volumes"}
	}
	if _, err := manager.run(ctx, databaseDir, composeArgs(databaseDir, args...)...); err != nil {
		return State{}, fmt.Errorf("database action failed; files and credentials were retained")
	}
	if action == "delete" {
		if err := os.RemoveAll(databaseDir); err != nil {
			return State{}, fmt.Errorf("database container removed but data cleanup failed; retry deletion")
		}
	}
	return manager.list(ctx, directory)
}
