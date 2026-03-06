# ⚠️ THIS PROJECT IS WORK IN PROGESS AND WILL NOT RUN ⚠️

A free and open source tool for easy game server deployment.

## TLS defaults

Worker TLS is enabled by default.

- If cert/key files are missing, the worker auto-generates:
  - CA cert/key: `/etc/vestri/certs/ca.crt`, `/etc/vestri/certs/ca.key`
  - Server cert/key: `/etc/vestri/certs/worker.crt`, `/etc/vestri/certs/worker.key`
- Default config file path: `/etc/vestri/settings.json`
- To let backend trust this CA, set backend env:
  - `WORKER_TLS_CA_CERT_FILE=/etc/vestri/certs/ca.crt`
