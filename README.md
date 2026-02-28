# GileBrowser

A lightweight HTTP file server with a clean web UI. Browse, preview, and download files and directories from one or more configured root paths.

## Features

- Serve multiple root directories over HTTP
- Universal preview page for every file and directory
- Directory downloads as ZIP archives
- Syntax highlighting for text files via Chroma (server-side rendered)
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

Flags take precedence over environment variables.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--port` | `GILE_PORT` | `7887` | HTTP port to listen on |
| `--dir` | `GILE_DIRS` | — | Root directory to serve (repeatable) |
| `--bandwidth` | `GILE_BANDWIDTH` | unlimited | Total upload cap shared across all clients (e.g. `10mbps`, `500kbps`, `1gbps`) |
| `--highlight-theme` | `GILE_HIGHLIGHT_THEME` | `catppuccin-mocha` | Chroma syntax-highlight theme |
| `--title` | `GILE_TITLE` | `GileBrowser` | Site name shown in the header and page titles |
| `--favicon` | `GILE_FAVICON` | — | Path to a custom favicon file (PNG, SVG, ICO, etc.) |
| `--default-theme` | `GILE_DEFAULT_THEME` | `dark` | Default UI colour scheme for first-time visitors: `dark` (Catppuccin Mocha) or `light` (Catppuccin Latte). Clients can override this with the in-page toggle, which is remembered in their browser. |

`GILE_DIRS` accepts colon-separated paths: `GILE_DIRS=/srv/a:/srv/b`

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
| `dracula` | Dracula |
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
