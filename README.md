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

`GILE_DIRS` accepts colon-separated paths: `GILE_DIRS=/srv/a:/srv/b`

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
