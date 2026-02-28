# Project Structure

## Overview
This is a file download server built in Go that serves files via HTTP. The server allows users to configure multiple root directories and provides a web UI for browsing and downloading files.

## Core Features

### Directory Management
- Accept multiple directories as root paths for the file server
- All supplied directories appear in the UI root
- Each directory root is accessible as a top-level path

### File Serving
- Serve files via HTTP with proper MIME types
- Maintain filesystem-like URL structure (e.g., `/test1/subdir/file.txt` => `http://localhost:7887/test1/subdir/file.txt`)
- Support for nested directory navigation
- **Directory Downloads**: Allow users to download entire directories as ZIP archives
- **Proper HTTP Headers**: Set Content-Length and other headers correctly to enable download progress tracking on client side

### File Previews
- **Image files** (PNG, JPEG, GIF, etc.): Render inline preview before download
- **Text files**: Render preview with syntax highlighting
- All previews are server-side rendered to minimize client-side dependencies

### UI/UX
- Modern but basic user interface
- Clean file/folder navigation
- Download buttons and preview options
- **No Emojis**: Do not use emojis anywhere in the UI

### File Search
- **Fuzzy Finder Style Search**: Built-in search that recurses all subdirectories from the CWD
- **Client-Side Indexing**: Search functionality operates on client-side with server-provided file index
- **Real-Time Results**: Instant filtering as user types with fuzzy matching algorithm

## Project Goals
- Keep codebase clean and maintainable
- Add new features only with explicit instruction
- Minimize client-side rendering reliance
- Server-side focused implementation
- **Zero external CDN dependencies** - All JS libraries, CSS frameworks, fonts, and other assets must be served locally from the server
- **Dynamic File Serving**: The fileserver must be dynamic and responsive to filesystem changes. When an admin adds, removes, or modifies files in the served directories, the server should immediately reflect these changes without requiring a restart. This includes updating directory listings, file metadata, and search indexes in real-time.

## Tech Stack
- **Language**: Go (Golang)
- **Web Framework**: [To be determined]
- **Templates**: Go html/template (server-side rendering)
- **Static Assets**: Embedded via Go embed
- **No External CDN Dependencies**: All JavaScript libraries, CSS frameworks, fonts, and other static assets must be bundled with the server and served locally. This ensures:
  - Works in air-gapped or restricted network environments
  - No third-party tracking or dependencies
  - Full control over asset versions and updates
  - Faster load times for local deployments

## Directory Structure
```
gileserver/
├── main.go                 # Application entry point
├── go.mod                  # Go module definition
├── go.sum                  # Go dependencies
├── AGENTS.md               # This file
├── CLAUDE.md               # This file
├── config/
│   └── config.go           # Configuration management
├── server/
│   ├── server.go           # HTTP server setup
│   └── routes.go           # Route definitions
├── handlers/
│   ├── file.go             # File serving handlers
│   ├── dir.go              # Directory listing handlers
│   └── preview.go          # File preview handlers
├── models/
│   └── file.go             # File/directory data structures
├── templates/
│   ├── base.html           # Base template
│   ├── index.html          # Root directory listing
│   ├── directory.html      # Subdirectory listing
│   ├── file-preview.html   # File preview page
│   └── image-preview.html  # Image preview template
└── static/
    ├── css/
    │   └── style.css       # Styles
    ├── js/
    │   └── main.js         # Minimal client-side scripts
    └── images/             # Static images
```

## Configuration
Server configuration prioritizes **command-line flags** as the primary method, with **environment variables** as a fallback when a corresponding flag is not provided:
- HTTP port (default: 7887)
- Root directory paths (multiple)
- Preview settings
- MIME type overrides

**Default Listening Behavior**: By default, the server listens on all network interfaces (0.0.0.0), making it accessible from all clients.

This approach ensures that explicit CLI arguments take precedence, while still allowing convenient environment-based configuration for optional or default values.

## Development Guidelines
1. **Clean Code**: Follow Go best practices (effective go, go style guide)
2. **Documentation**: Keep README and inline docs updated
3. **Error Handling**: Proper error propagation and user-friendly messages
4. **Security**: Validate file paths, prevent directory traversal attacks
5. **Performance**: Handle large files efficiently, proper streaming
