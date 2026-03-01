# GileBrowser

A lightweight HTTP file server with a clean web UI. Browse, preview, and download files from one or more configured root directories.

- Multiple root directories served from a single instance
- File and directory previews (images, syntax-highlighted text, rendered Markdown/Org/HTML)
- Directory downloads as ZIP archives
- Fuzzy file search across all served directories
- Bandwidth limiting
- Download statistics persisted to disk
- All assets embedded in the binary — no runtime dependencies

## Table of Contents

- [Usage](#usage)
- [Configuration](#configuration)
- [Reverse Proxy](#reverse-proxy)
- [Docker](#docker)
- [Building](#building)
- [Troubleshooting](#troubleshooting)

---

## Usage

```sh
gilebrowser --dir /path/to/files
```

Multiple directories:

```sh
gilebrowser --dir /srv/media --dir /srv/docs
```

Custom port and theme:

```sh
gilebrowser --port 8080 --highlight-theme catppuccin-latte --dir /srv/files
```

---

## Configuration

Flags take precedence over environment variables. All preview flags default to `true`.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--port` | `GILE_PORT` | `7887` | HTTP port to listen on |
| `--dir` | `GILE_DIRS` | — | Root directory to serve (repeatable; env is colon-separated) |
| `--bandwidth` | `GILE_BANDWIDTH` | unlimited | Server-wide upload cap, e.g. `10mbps`, `500kbps`, `1gbps` |
| `--title` | `GILE_TITLE` | `GileBrowser` | Site name shown in the header and page titles |
| `--default-theme` | `GILE_DEFAULT_THEME` | `dark` | Default UI colour scheme: `dark` or `light`. Overridable per-client via the in-page toggle. |
| `--highlight-theme` | `GILE_HIGHLIGHT_THEME` | `catppuccin-mocha` | Chroma syntax-highlight theme |
| `--favicon` | `GILE_FAVICON` | — | Path to a custom favicon (PNG, SVG, ICO, etc.) |
| `--stats-dir` | `GILE_STATS_DIR` | current working directory | Directory where `gile.json` is written. Created on startup if absent. |
| `--preview-images` | `GILE_PREVIEW_IMAGES` | `true` | Render image files inline |
| `--preview-text` | `GILE_PREVIEW_TEXT` | `true` | Render text and code files with syntax highlighting |
| `--preview-docs` | `GILE_PREVIEW_DOCS` | `true` | Render Markdown, Org-mode, and HTML files as documents. Falls back to syntax highlighting if `--preview-text` is enabled, otherwise shows an info card. |
| `--trusted-proxy` | `GILE_TRUSTED_PROXY` | — | IP address or CIDR of a trusted reverse proxy (e.g. `127.0.0.1` or `10.0.0.0/8`). When set, `X-Real-IP` and `X-Forwarded-For` headers from that proxy are used for rate limiting and access logs. Leave unset for direct access. |

`GILE_DIRS` accepts colon-separated paths: `GILE_DIRS=/srv/a:/srv/b`

> **Symlinks:** By default, symlinks inside served directories are followed regardless of where they point, including targets outside the configured root. This is intentional — it allows administrators to include files or directories from anywhere on the system by creating symlinks inside a served root. If you are serving untrusted content or want to restrict access strictly to the declared root paths, ensure that no symlinks pointing outside those roots exist in the served directories.

Boolean options accept: `true`, `false`, `1`, `0`, `yes`, `no`, `on`, `off` (case-insensitive).

<details>
<summary>Preview behaviour matrix</summary>

| `--preview-images` | `--preview-text` | `--preview-docs` | Images | Text / code | Markdown / Org / HTML |
|--------------------|------------------|------------------|--------|-------------|-----------------------|
| `true` | `true` | `true` | Inline | Highlighted | Rendered |
| `true` | `true` | `false` | Inline | Highlighted | Highlighted |
| `true` | `false` | `true` | Inline | Info card | Info card |
| `true` | `false` | `false` | Inline | Info card | Info card |
| `false` | `true` | `true` | Info card | Highlighted | Rendered |
| `false` | `true` | `false` | Info card | Highlighted | Highlighted |
| `false` | `false` | `false` | Info card | Info card | Info card |

</details>

<details>
<summary>Available highlight themes</summary>

| Theme | Style |
|-------|-------|
| `catppuccin-mocha` | Dark — muted blue (default) |
| `catppuccin-macchiato` | Dark — slightly warmer |
| `catppuccin-frappe` | Medium dark |
| `catppuccin-latte` | Light |
| `dracula` | Dark purple |
| `monokai` | Classic dark |
| `github` | Light |
| `github-dark` | Dark |
| `nord` | Arctic dark |
| `nordic` | Nord variant |
| `onedark` | One Dark |
| `tokyonight-night` | Tokyo Night |
| `tokyonight-storm` | Tokyo Night Storm |
| `tokyonight-moon` | Tokyo Night Moon |
| `tokyonight-day` | Light |
| `gruvbox` | Dark warm |
| `gruvbox-light` | Light warm |
| `rose-pine` | Dark |
| `rose-pine-moon` | Dark variant |
| `rose-pine-dawn` | Light |
| `solarized-dark` | Solarized Dark |
| `solarized-light` | Solarized Light |
| `vim` | Vim default |
| `emacs` | Emacs default |
| `xcode` | Light |
| `xcode-dark` | Dark |

Any name from Chroma's full style registry is accepted.

</details>

---

## Reverse Proxy

GileBrowser works behind NGINX or Nginx Proxy Manager (NPM) but requires a few settings beyond a plain `proxy_pass`. The two critical ones are:

- **`proxy_buffering off`** — without this, NGINX buffers the entire response body in memory (or on disk) before forwarding it to the client. For large file downloads and ZIP archives this causes the client to see no progress until NGINX finishes buffering, and for very large files it can exhaust NGINX's buffer limits and terminate the download.
- **`proxy_read_timeout`** — the default is 60 seconds. A large file served at a limited bandwidth rate will take far longer than that, causing NGINX to close the connection mid-download. Set this to a value larger than your longest expected download time.
- **`proxy_request_buffering off`** — prevents NGINX from buffering request bodies, keeping the pipeline clean for HTTP/1.1 keep-alive connections.

Always set `--trusted-proxy` (or `GILE_TRUSTED_PROXY`) to your proxy's IP so that GileBrowser's rate limiter and access logs see real client IPs rather than the proxy address.

<details>
<summary>NGINX — direct configuration</summary>

```nginx
upstream gilebrowser {
    server 127.0.0.1:7887;
    keepalive 32;
}

server {
    listen 443 ssl;
    server_name files.example.com;

    # --- SSL (adjust paths to your certificate) ---
    ssl_certificate     /etc/ssl/certs/files.example.com.crt;
    ssl_certificate_key /etc/ssl/private/files.example.com.key;

    # --- Proxy settings ---
    location / {
        proxy_pass http://gilebrowser;

        proxy_http_version 1.1;
        proxy_set_header Connection        "";          # enable keepalive to upstream
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Disable response buffering so downloads stream directly to the client.
        # Without this, NGINX holds the entire file in memory before forwarding,
        # breaking progress indicators and potentially OOM-killing NGINX on large
        # files or ZIP archives.
        proxy_buffering             off;
        proxy_request_buffering     off;

        # Allow long-running downloads. Adjust to suit your largest files and
        # slowest bandwidth limit. 0 disables the timeout entirely if preferred.
        proxy_read_timeout          3600s;
        proxy_send_timeout          3600s;

        # Let upstream set Content-Length; do not re-encode.
        gzip off;
    }
}
```

Then start GileBrowser with the proxy's address as the trusted source:

```sh
gilebrowser --trusted-proxy 127.0.0.1 --dir /srv/files
```

</details>

<details>
<summary>Nginx Proxy Manager — custom Nginx configuration</summary>

In NPM, open your proxy host, go to the **Advanced** tab, and paste the following into the **Custom Nginx Configuration** box. NPM generates the `proxy_pass`, `Host`, and SSL blocks automatically — this snippet only adds the download-specific overrides.

```nginx
proxy_buffering         off;
proxy_request_buffering off;
proxy_read_timeout      3600s;
proxy_send_timeout      3600s;
proxy_http_version      1.1;
proxy_set_header        Connection "";
proxy_set_header        X-Real-IP         $remote_addr;
proxy_set_header        X-Forwarded-For   $proxy_add_x_forwarded_for;
gzip off;
```

Then start GileBrowser with NPM's internal Docker network address as the trusted proxy. If GileBrowser is running in Docker on the same `npm_default` network the NPM container IP is typically in the `172.x.x.x` range — use the subnet:

```sh
# Docker run
docker run -d \
  -e GILE_DIRS=/data/files \
  -e GILE_TRUSTED_PROXY=172.16.0.0/12 \
  ...

# Docker Compose (add to the environment block)
environment:
  GILE_TRUSTED_PROXY: "172.16.0.0/12"
```

> **Finding your exact subnet:** run `docker network inspect npm_default` (or whatever network NPM and GileBrowser share) and use the `Subnet` value from the `IPAM.Config` section.

</details>

---

## Docker

### Quick start

```sh
docker run -d \
  --name gilebrowser \
  -p 7887:7887 \
  -v /srv/files:/data/files \
  -e GILE_DIRS=/data/files \
  docker.io/justinlime/gilebrowser:latest
```

<details>
<summary>Full example</summary>

```sh
docker run -d \
  --name gilebrowser \
  --restart unless-stopped \
  -p 7887:7887 \
  -v /srv/media:/data/media:ro \
  -v /srv/docs:/data/docs:ro \
  -v /srv/stats:/data/stats \
  -e GILE_DIRS=/data/media:/data/docs \
  -e GILE_PORT=7887 \
  -e GILE_TITLE=MyFiles \
  -e GILE_DEFAULT_THEME=dark \
  -e GILE_HIGHLIGHT_THEME=catppuccin-mocha \
  -e GILE_BANDWIDTH=100mbps \
  -e GILE_STATS_DIR=/data/stats \
  -e GILE_PREVIEW_IMAGES=true \
  -e GILE_PREVIEW_TEXT=true \
  -e GILE_PREVIEW_DOCS=true \
  docker.io/justinlime/gilebrowser:latest
```

</details>

<details>
<summary>Docker Compose</summary>

```yaml
services:
  gilebrowser:
    image: docker.io/justinlime/gilebrowser:latest
    container_name: gilebrowser
    restart: unless-stopped
    ports:
      - "7887:7887"
    volumes:
      - /srv/media:/data/media:ro
      - /srv/docs:/data/docs:ro
      - /srv/stats:/data/stats
    environment:
      GILE_DIRS: /data/media:/data/docs
      GILE_PORT: 7887
      GILE_TITLE: MyFiles
      GILE_DEFAULT_THEME: dark
      GILE_HIGHLIGHT_THEME: catppuccin-mocha
      GILE_BANDWIDTH: 100mbps
      GILE_STATS_DIR: /data/stats
      GILE_PREVIEW_IMAGES: "true"
      GILE_PREVIEW_TEXT: "true"
      GILE_PREVIEW_DOCS: "true"
```

</details>

Mount served directories with `:ro` when writes are not needed. To persist download stats across restarts, mount a volume and point `GILE_STATS_DIR` at it.

### Building locally

```sh
docker build -t gilebrowser .
docker run -d -p 7887:7887 -v /srv/files:/data -e GILE_DIRS=/data gilebrowser
```

---

## Building

Requires Go 1.25+.

```sh
go build -o gilebrowser .
```

---

## Troubleshooting

<details>
<summary>Watcher: inotify watch limit reached</summary>

GileBrowser watches served directories with `inotify` for instant cache invalidation. Each subdirectory consumes one watch descriptor, and the kernel enforces a per-user limit via `fs.inotify.max_user_watches` (default: **8,192** on most distributions).

When the limit is hit, the following is logged:

```
watcher: inotify watch limit reached (stopped at <path>).
  Directories beyond this point will not receive instant cache invalidation;
  the 20m0s safety TTL will still correct any stale entries.
```

Unwatched directories fall back to a 20-minute cache TTL. The server continues to function normally.

Raise the limit on the host (cannot be changed from inside a container):

```sh
# Temporary
sudo sysctl -w fs.inotify.max_user_watches=524288

# Permanent
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

Check the current limit:

```sh
cat /proc/sys/fs/inotify/max_user_watches
```

</details>
