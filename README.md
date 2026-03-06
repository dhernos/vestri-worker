# ⚠️ THIS PROJECT IS WORK IN PROGESS AND WILL NOT RUN ⚠️

A free and open source tool for easy game server deployment.

## Run

```bash
go run ./cmd/worker
```

Worker settings file is loaded from:

- `/etc/vestri/settings.json`

If missing, defaults are generated automatically.

## TLS defaults (v1.0)

Worker TLS is enabled by default.

- If cert/key files are missing, the worker auto-generates:
  - CA cert/key: `/etc/vestri/certs/ca.crt`, `/etc/vestri/certs/ca.key`
  - Server cert/key: `/etc/vestri/certs/worker.crt`, `/etc/vestri/certs/worker.key`
- Default config file path: `/etc/vestri/settings.json`
- Add worker CA certs to backend:
  - `vestri-backend/certs/worker-cas/*.crt`
  - Backend loads all `.crt/.pem/.cer` files in that directory.

## Minimal settings example (HTTPS default)

```json
{
  "useTLS": true,
  "TLSCert": "/etc/vestri/certs/worker.crt",
  "TLSKey": "/etc/vestri/certs/worker.key",
  "tls_ca_cert": "/etc/vestri/certs/ca.crt",
  "tls_ca_key": "/etc/vestri/certs/ca.key",
  "tls_auto_generate": true,
  "tls_sans": ["worker.example.com", "10.0.0.12"],
  "http_port": ":8031",
  "require_tls": true,
  "trust_proxy_headers": false
}
```

## Explicit HTTP fallback

HTTP is still possible if you intentionally disable TLS:

```json
{
  "useTLS": false,
  "require_tls": false
}
```

Then set node base URL explicitly to `http://...` in backend.
