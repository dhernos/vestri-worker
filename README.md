# Vestri Worker

Worker documentation lives in the main repository:

- https://github.com/dhernos/vestri

## Request Rate Limits

The worker defaults to 100 requests per second with a burst allowance of 200 per
authenticated API key. This accommodates the filesystem request bursts used when
provisioning dynamic Minecraft servers. Rate limiting and authentication remain
enabled; BlueMap assets retain their separate budget described below.

Existing settings are not overwritten on upgrade. On native installations, update
these fields in `/etc/vestri/settings.json` if they still contain the old 10/20
values, preserving all other settings:

```json
"rate_limit_rps": 100,
"rate_limit_burst": 200
```

Restart the worker after changing its settings:

```sh
sudo systemctl restart vestri_worker.service
```

Omitted fields use the defaults from the installed worker version. Explicit custom
values remain unchanged.

## Managed Plugin Databases

The worker embeds MariaDB 11.4 and PostgreSQL 17 Compose templates in `internal/database/templates`. Managed databases join the game's existing Docker network without publishing host ports. Docker Compose with JSON configuration/status support is required.

Compose files and credentials are stored outside the server file-browser root, in `.vestri-databases-<hash>` beside `fs_base_path`. The worker needs permission to create this private directory and to manage Docker containers and volumes. Database contents are persistent named volumes, not part of game-server file backups. Stop retains volumes; confirmed deletion removes them. Back up database contents separately using database-native tools.

Run the optional isolated Docker integration test with `VESTRI_DATABASE_DOCKER_TESTS=1 go test ./internal/database -run TestDatabaseDockerIntegration -v`. It creates temporary containers and a network, verifies internal DNS/plugin-user access and absent host-port bindings, and cleans up afterward.

## BlueMap

The authenticated `/stack/bluemap/status` and `/stack/bluemap/view/` routes proxy BlueMap for Paper/Purpur servers. The backend supplies the authorized stack and server root; plugin configuration is read from `data/plugins/BlueMap/webserver.conf`. The standard generated configuration with literal `enabled`/`port` settings is supported. The worker discovers the running Compose service's container IP and connects directly to BlueMap's internal port; do not publish that port or add router forwarding.

Run the worker on the Docker host with access to its bridge networks. Host networking and unreachable remote Docker networks are not supported. Map assets use a separate rate-limit bucket at 20 times the configured rate/burst. Requests remain signed; panel cookies and worker authentication headers are not sent to BlueMap. Deploy matching frontend/backend/worker versions.
