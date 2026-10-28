# Vestri Worker

Worker documentation lives in the main repository:

- https://github.com/dhernos/vestri

## Managed Plugin Databases

The worker embeds MariaDB 11.4 and PostgreSQL 17 Compose templates in `internal/database/templates`. Managed databases join the game's existing Docker network without publishing host ports. Docker Compose with JSON configuration/status support is required.

Compose files and credentials are stored outside the server file-browser root, in `.vestri-databases-<hash>` beside `fs_base_path`. The worker needs permission to create this private directory and to manage Docker containers and volumes. Database contents are persistent named volumes, not part of game-server file backups. Stop retains volumes; confirmed deletion removes them. Back up database contents separately using database-native tools.

Run the optional isolated Docker integration test with `VESTRI_DATABASE_DOCKER_TESTS=1 go test ./internal/database -run TestDatabaseDockerIntegration -v`. It creates temporary containers and a network, verifies internal DNS/plugin-user access and absent host-port bindings, and cleans up afterward.
