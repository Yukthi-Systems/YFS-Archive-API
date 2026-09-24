# YFS Archive API

Turn any YFS folder tree into a downloadable **ZIP** or **TAR** archive,
streamed straight from your storage servers, with live progress over
Server-Sent Events.

- **Source code:** https://github.com/Yukthi-Systems/YFS-Archive-API
- **Issues:** https://github.com/Yukthi-Systems/YFS-Archive-API/issues
- **License:** GPL-3.0
- **Maintained by:** Yukthi Systems

---

## Highlights

- Static Go binary on **Alpine 3.22**, small image, no CGO
- **Streaming end to end.** Files are never held in memory as a whole.
- **ZIP (Deflate) or plain TAR** output, selected per request
- Live progress over **SSE**, and **token-protected**, auto-expiring downloads
- Fixed worker pool, bounded queue and a hard archive size cap
- **Graceful shutdown.** Running jobs finish before the container exits.
- Built-in `HEALTHCHECK` on `/healthz`
- **Read-only** against PostgreSQL. It never writes or migrates.

---

## Quick start

### docker run

```bash
docker run -d --name yfs-archive-api \
  --env-file .env \
  -p 8080:8080 \
  -v yfs-archives:/app/archives \
  --stop-timeout 45 \
  rjyspl/yfs-archive-api:latest
```

The minimum `.env`:

```dotenv
DATABASE_URL=postgres://archive_readonly:change-me@db-host:5432/yfs
```

### docker compose

```yaml
services:
  archive-service:
    image: rjyspl/yfs-archive-api:latest
    restart: unless-stopped
    env_file: .env
    ports:
      - "8080:8080"
    volumes:
      - archives:/app/archives
    stop_grace_period: 45s   # must exceed SERVER_SHUTDOWN_TIMEOUT

volumes:
  archives:
```

```bash
docker compose up -d
curl http://localhost:8080/healthz   # {"status":"ok"}
curl http://localhost:8080/readyz    # {"status":"ready"}
```

---

## Requirements

- **PostgreSQL** with the `folders`, `files`, `file_versions` and `servers`
  tables. The full expected schema is in the
  [README](https://github.com/Yukthi-Systems/YFS-Archive-API#database-schema).
  Use a **`SELECT`-only** role.
- At least one row in `servers`. Storage server addresses and API keys are
  read from that table at startup, not from environment variables.

---

## Configuration

Pass variables with `--env-file`, `-e` or `environment:`. Only
`DATABASE_URL` is required, because the image already sets
`ARCHIVE_TEMP_DIR`.

### Image defaults

| Variable           | Value in image                   |
|--------------------|----------------------------------|
| `APP_ENV`          | `production`                     |
| `SERVER_HOST`      | `0.0.0.0`                        |
| `SERVER_PORT`      | `8080`                           |
| `LOG_OUTPUT`       | `stdout`                         |
| `LOG_FILE`         | `/app/logs/archive-service.log`  |
| `ARCHIVE_TEMP_DIR` | `/app/archives`                  |

### Common settings

| Variable                   | Default       | Notes |
|----------------------------|---------------|-------|
| `DATABASE_URL`             | —             | **Required.** PostgreSQL connection string |
| `SERVER_PUBLIC_BASE_URL`   | *(empty)*     | Public origin for absolute `download_url`s, e.g. `https://archive.example.com` |
| `SERVER_SHUTDOWN_TIMEOUT`  | `30s`         | Keep below the container stop timeout |
| `ARCHIVE_MAX_SIZE_BYTES`   | `4294967296`  | 4 GiB hard cap |
| `ARCHIVE_EXPIRY`           | `1h`          | How long an archive and its token stay valid |
| `ARCHIVE_WORKERS`          | `4`           | Concurrent archive jobs |
| `ARCHIVE_QUEUE_SIZE`       | `100`         | Further requests get `503` |
| `ARCHIVE_CLEANUP_INTERVAL` | `10m`         | Expired-archive sweep interval |
| `STORAGE_REQUEST_TIMEOUT`  | `30s`         | Time to response headers from a storage server |
| `DATABASE_MAX_CONNS`       | `20`          | Pool size |
| `LOG_LEVEL`                | `info`        | `debug`, `info`, `warn`, `error` |

The full list is in the
[configuration reference](https://github.com/Yukthi-Systems/YFS-Archive-API#configuration).

---

## Volumes

| Path             | Purpose |
|------------------|---------|
| `/app/archives`  | Finished archives waiting for download. Size it for your largest archive × concurrent jobs. |
| `/app/logs`      | Only used when `LOG_OUTPUT=file`. |

Logs are JSON on stdout by default, so `docker logs` works out of the box.

---

## Endpoints

| Method & path                              | Purpose |
|--------------------------------------------|---------|
| `POST /internal/archives`                  | Queue an archive job. **Internal only — do not expose publicly.** |
| `GET  /archives/{job_id}/events`           | Live progress (Server-Sent Events) |
| `GET  /archives/{job_id}/download?token=…` | Download the finished ZIP or TAR |
| `GET  /healthz`                            | Liveness |
| `GET  /readyz`                             | Readiness. Returns `503` during shutdown. |

```bash
curl -X POST http://localhost:8080/internal/archives \
  -H 'Content-Type: application/json' \
  -d '{"root_folder_id":"<uuid>","archive_name":"Documents.zip","export_type":"zip"}'
```

See the [API reference](https://github.com/Yukthi-Systems/YFS-Archive-API#api-reference)
for request and response details.

---

## Graceful shutdown

On `SIGTERM` the container:

1. flips `/readyz` to `503`,
2. rejects new archive requests,
3. lets running jobs finish, for up to `SERVER_SHUTDOWN_TIMEOUT`,
4. closes the HTTP server.

Give Docker more time than that: `stop_grace_period: 45s` or
`docker stop -t 45`.

---

## Scaling

Job state, download tokens and archive files are **per instance** in this
version. When running several replicas, enable **sticky routing on
`job_id`** so a client's SSE and download requests reach the instance that
created the job.

---

## Tags

| Tag      | Description |
|----------|-------------|
| `latest` | Latest build from `main` |
| `x.y.z`  | Tagged releases (recommended for production) |

### Build it yourself

```bash
git clone https://github.com/Yukthi-Systems/YFS-Archive-API.git
cd YFS-Archive-API
docker build --build-arg VERSION=1.0.0 -t rjyspl/yfs-archive-api:1.0.0 .
```

---

## Community and support

- 💬 Discord: <a href="https://discord.com/invite/2BS7Z4FhJ" target="_blank" rel="noopener noreferrer">discord.com/invite/2BS7Z4FhJ</a>
- 📧 Email: [connect@yukthi.com](mailto:connect@yukthi.com)
- 🐛 Issues: [GitHub Issues](https://github.com/Yukthi-Systems/YFS-Archive-API/issues)

## License

Licensed under the
[GNU General Public License v3.0](https://github.com/Yukthi-Systems/YFS-Archive-API/blob/main/LICENSE).
As with all Docker images, the image also contains other software (Alpine
base, BusyBox, CA certificates) under its own licenses.
