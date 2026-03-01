# GileBrowser

A lightweight HTTP file server with a clean web UI. Browse, preview, and download files and directories from one or more configured root paths.

## Features

- Serve multiple root directories over HTTP
- Universal preview page for every file and directory
- Directory downloads as ZIP archives
- Syntax highlighting for text files via Chroma (server-side rendered)
- Rich document rendering for Markdown, Org-mode, and HTML files
- Inline image previews
- Fuzzy file search across all served directories
- All assets embedded in the binary — no runtime dependencies

## Usage

```sh
gilebrowser --dir /path/to/files
```

Serve multiple directories:

```sh
gilebrowser --dir /srv/media --dir /srv/docs
```

Custom port and highlight theme:

```sh
gilebrowser --port 8080 --highlight-theme catppuccin-latte --dir /srv/files
```

## Configuration

Flags take precedence over environment variables. All preview types are **enabled by default**.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--port` | `GILE_PORT` | `7887` | HTTP port to listen on |
| `--dir` | `GILE_DIRS` | — | Root directory to serve (repeatable; env is colon-separated) |
| `--bandwidth` | `GILE_BANDWIDTH` | unlimited | Total upload cap shared across all clients (e.g. `10mbps`, `500kbps`, `1gbps`) |
| `--highlight-theme` | `GILE_HIGHLIGHT_THEME` | `catppuccin-mocha` | Chroma syntax-highlight theme |
| `--title` | `GILE_TITLE` | `GileBrowser` | Site name shown in the header and page titles |
| `--favicon` | `GILE_FAVICON` | — | Path to a custom favicon file (PNG, SVG, ICO, etc.) |
| `--default-theme` | `GILE_DEFAULT_THEME` | `dark` | Default UI colour scheme for first-time visitors: `dark` (Catppuccin Mocha) or `light` (Catppuccin Latte). Clients can override this with the in-page toggle, which is remembered in their browser. |
| `--stats-file` | `GILE_STATS_FILE` | `gilebrowser-stats.json` | Path to the JSON file used to persist download statistics (total downloads and bytes served) across restarts. Created automatically on startup if it does not exist. |
| `--preview-images` | `GILE_PREVIEW_IMAGES` | `true` | Enable inline image previews. When `false`, image files show a download info card instead. |
| `--preview-text` | `GILE_PREVIEW_TEXT` | `true` | Enable syntax-highlighted text and code previews. When `false`, all text files show a download info card instead. |
| `--preview-docs` | `GILE_PREVIEW_DOCS` | `true` | Enable rich rendered document previews for Markdown (`.md`), Org-mode (`.org`), and HTML (`.html`) files. When `false`, those files fall back to syntax highlighting (if `--preview-text` is enabled) or the download info card. Has no effect if `--preview-text` is also `false`. |

`GILE_DIRS` accepts colon-separated paths: `GILE_DIRS=/srv/a:/srv/b`

Boolean flags and environment variables accept: `true`, `false`, `1`, `0`, `yes`, `no`, `on`, `off` (case-insensitive).

### Preview behaviour matrix

| `--preview-images` | `--preview-text` | `--preview-docs` | Image files | Text / code files | Markdown / Org / HTML |
|--------------------|------------------|------------------|-------------|-------------------|-----------------------|
| `true` | `true` | `true` | Inline image | Syntax highlighted | Rendered document |
| `true` | `true` | `false` | Inline image | Syntax highlighted | Syntax highlighted |
| `true` | `false` | `true` | Inline image | Info card | Info card |
| `true` | `false` | `false` | Inline image | Info card | Info card |
| `false` | `true` | `true` | Info card | Syntax highlighted | Rendered document |
| `false` | `true` | `false` | Info card | Syntax highlighted | Syntax highlighted |
| `false` | `false` | `false` | Info card | Info card | Info card |

## Docker

### Quick start

```sh
docker run -d \
  --name gilebrowser \
  -p 7887:7887 \
  -v /srv/files:/data/files \
  -e GILE_DIRS=/data/files \
  ghcr.io/justinlime/gilebrowser:latest
```

### Full example with all options

```sh
docker run -d \
  --name gilebrowser \
  --restart unless-stopped \
  -p 7887:7887 \
  -v /srv/media:/data/media:ro \
  -v /srv/docs:/data/docs:ro \
  -e GILE_DIRS=/data/media:/data/docs \
  -e GILE_PORT=7887 \
  -e GILE_TITLE=MyFiles \
  -e GILE_DEFAULT_THEME=dark \
  -e GILE_HIGHLIGHT_THEME=catppuccin-mocha \
  -e GILE_BANDWIDTH=100mbps \
  -e GILE_STATS_FILE=/data/stats/gilebrowser-stats.json \
  -e GILE_PREVIEW_IMAGES=true \
  -e GILE_PREVIEW_TEXT=true \
  -e GILE_PREVIEW_DOCS=true \
  -v /srv/stats:/data/stats \
  ghcr.io/justinlime/gilebrowser:latest
```

### Docker Compose

```yaml
services:
  gilebrowser:
    image: ghcr.io/justinlime/gilebrowser:latest
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
      GILE_STATS_FILE: /data/stats/gilebrowser-stats.json
      GILE_PREVIEW_IMAGES: "true"
      GILE_PREVIEW_TEXT: "true"
      GILE_PREVIEW_DOCS: "true"
```

> **Tip:** Mount served directories with `:ro` (read-only) when the server only needs to serve them, preventing accidental modification from inside the container.

> **Stats persistence:** Mount a dedicated volume (e.g. `/srv/stats:/data/stats`) and set `GILE_STATS_FILE` to a path inside it so download statistics survive container restarts and image upgrades.

### Building the image locally

```sh
docker build -t gilebrowser .
docker run -d -p 7887:7887 -v /srv/files:/data -e GILE_DIRS=/data gilebrowser
```

## Available themes

| Theme | Description |
|-------|-------------|
| `catppuccin-mocha` | Catppuccin Mocha (default) — dark, muted blue |
| `catppuccin-macchiato` | Catppuccin Macchiato — dark, slightly warmer |
| `catppuccin-frappe` | Catppuccin Frappe — medium-dark |
| `catppuccin-latte` | Catppuccin Latte — light |
| `dracula` | Dracula — dark purple |
| `monokai` | Monokai — classic dark |
| `github` | GitHub — light |
| `github-dark` | GitHub Dark |
| `nord` | Nord — arctic dark |
| `nordic` | Nordic — nord variant |
| `onedark` | One Dark |
| `tokyonight-night` | Tokyo Night |
| `tokyonight-storm` | Tokyo Night Storm |
| `tokyonight-moon` | Tokyo Night Moon |
| `tokyonight-day` | Tokyo Night Day — light |
| `gruvbox` | Gruvbox — dark warm |
| `gruvbox-light` | Gruvbox — light warm |
| `rose-pine` | Rosé Pine — dark |
| `rose-pine-moon` | Rosé Pine Moon |
| `rose-pine-dawn` | Rosé Pine Dawn — light |
| `solarized-dark` | Solarized Dark |
| `solarized-light` | Solarized Light |
| `vim` | Vim default |
| `emacs` | Emacs default |
| `xcode` | Xcode — light |
| `xcode-dark` | Xcode Dark |

Any other name from Chroma's full style registry is also accepted.

## Building

```sh
go build -o gilebrowser .
```

Requires Go 1.25+.

## Troubleshooting

### Watcher: inotify watch limit reached

GileBrowser uses the Linux kernel's `inotify` interface to watch served directories for filesystem changes, enabling instant cache invalidation without polling. Each watched directory consumes one inotify watch descriptor, and the kernel enforces a per-user limit via `fs.inotify.max_user_watches`.

The default on most distributions (Ubuntu, Alpine, Debian, etc.) is **8,192**. If a served directory tree contains more subdirectories than this limit allows — or if the budget is shared with other running processes (editors, build tools, etc.) — you will see this message in the server log:

```
watcher: inotify watch limit reached (stopped at <path>).
  Directories beyond this point will not receive instant cache invalidation;
  the 20m0s safety TTL will still correct any stale entries.
```

**Impact:** Directories that could not be watched fall back to a 20-minute periodic cache refresh. The server continues to function normally; only the immediacy of cache invalidation is reduced for those paths.

**Fix:** Raise the limit on the host machine. This cannot be changed from inside a Docker container as it is a kernel-level parameter that must be set on the host.

To raise it temporarily (resets on reboot):

```sh
sudo sysctl -w fs.inotify.max_user_watches=524288
```

To make it permanent:

```sh
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

The value `524288` is a common recommendation and covers most use cases. For extremely large trees (such as `/nix/store`) an even higher value may be needed. To check the current limit:

```sh
cat /proc/sys/fs/inotify/max_user_watches
```
