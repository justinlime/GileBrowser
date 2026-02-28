// Package config handles all server configuration.
// CLI flags take precedence; environment variables are used as fallback.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Config holds the complete server configuration.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port int
	// Dirs is the ordered list of root directories to serve.
	Dirs []string
	// Theme is the Chroma syntax-highlighting theme name.
	Theme string
	// Title is the branding name shown in the UI and page titles.
	Title string
	// FaviconPath is an optional path to a custom favicon file.
	// When empty the server returns a minimal default favicon.
	FaviconPath string
	// BandwidthLimit is the total server-wide upload cap in bytes per second.
	// 0 means unlimited.
	BandwidthLimit float64
	// DefaultTheme is the UI colour scheme served to clients that have not
	// expressed a preference yet.  Accepted values: "dark", "light".
	DefaultTheme string
}

// dirList is a custom flag.Value that can be set multiple times.
type dirList []string

func (d *dirList) String() string {
	return strings.Join(*d, ", ")
}

func (d *dirList) Set(value string) error {
	*d = append(*d, value)
	return nil
}

// Load parses flags and environment variables, returning a validated Config.
func Load() (*Config, error) {
	var dirs dirList
	portFlag         := flag.Int("port", 0, "HTTP port to listen on (env: GILE_PORT, default: 7887)")
	themeFlag        := flag.String("highlight-theme", "", "Chroma syntax-highlight theme (env: GILE_HIGHLIGHT_THEME, default: catppuccin-mocha)")
	titleFlag        := flag.String("title", "", "Site branding title (env: GILE_TITLE, default: GileBrowser)")
	faviconFlag      := flag.String("favicon", "", "Path to a custom favicon file (env: GILE_FAVICON)")
	bandwidthFlag    := flag.String("bandwidth", "", "Total upload bandwidth cap, e.g. 10mbps, 500kbps, 1gbps (env: GILE_BANDWIDTH, default: unlimited)")
	defaultThemeFlag := flag.String("default-theme", "", "Default UI theme for new visitors: dark or light (env: GILE_DEFAULT_THEME, default: dark)")
	flag.Var(&dirs, "dir", "Root directory to serve (repeatable; env: GILE_DIRS, colon-separated)")
	flag.Parse()

	// --- port ---
	port := *portFlag
	if port == 0 {
		// fall back to env
		if v := os.Getenv("GILE_PORT"); v != "" {
			p, err := strconv.Atoi(v)
			if err != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("invalid GILE_PORT value %q", v)
			}
			port = p
		} else {
			port = 7887
		}
	}

	// --- dirs ---
	if len(dirs) == 0 {
		// fall back to env
		if v := os.Getenv("GILE_DIRS"); v != "" {
			for _, d := range strings.Split(v, ":") {
				d = strings.TrimSpace(d)
				if d != "" {
					dirs = append(dirs, d)
				}
			}
		}
	}

	// Remaining positional arguments are also treated as directories
	for _, arg := range flag.Args() {
		dirs = append(dirs, arg)
	}

	// --- highlight-theme ---
	theme := *themeFlag
	if theme == "" {
		if v := os.Getenv("GILE_HIGHLIGHT_THEME"); v != "" {
			theme = v
		} else {
			theme = "catppuccin-mocha"
		}
	}

	// --- title ---
	title := *titleFlag
	if title == "" {
		if v := os.Getenv("GILE_TITLE"); v != "" {
			title = v
		} else {
			title = "GileBrowser"
		}
	}

	// --- favicon ---
	favicon := *faviconFlag
	if favicon == "" {
		favicon = os.Getenv("GILE_FAVICON")
	}
	if favicon != "" {
		info, err := os.Stat(favicon)
		if err != nil {
			return nil, fmt.Errorf("favicon %q: %w", favicon, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("favicon %q is a directory, not a file", favicon)
		}
	}

	if len(dirs) == 0 {
		return nil, fmt.Errorf("at least one root directory must be specified via -dir flag, GILE_DIRS env var, or positional argument")
	}

	// --- default-theme ---
	defaultTheme := *defaultThemeFlag
	if defaultTheme == "" {
		if v := os.Getenv("GILE_DEFAULT_THEME"); v != "" {
			defaultTheme = v
		} else {
			defaultTheme = "dark"
		}
	}
	defaultTheme = strings.ToLower(strings.TrimSpace(defaultTheme))
	if defaultTheme != "dark" && defaultTheme != "light" {
		return nil, fmt.Errorf("invalid --default-theme %q: must be \"dark\" or \"light\"", defaultTheme)
	}

	// Validate that all supplied directories exist
	for _, d := range dirs {
		info, err := os.Stat(d)
		if err != nil {
			return nil, fmt.Errorf("directory %q: %w", d, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%q is not a directory", d)
		}
	}

	// --- bandwidth ---
	bwRaw := *bandwidthFlag
	if bwRaw == "" {
		bwRaw = os.Getenv("GILE_BANDWIDTH")
	}
	var bandwidthBps float64
	if bwRaw != "" {
		bps, err := parseBandwidth(bwRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid bandwidth %q: %w", bwRaw, err)
		}
		bandwidthBps = bps
	}

	return &Config{
		Port:           port,
		Dirs:           []string(dirs),
		Theme:          theme,
		Title:          title,
		FaviconPath:    favicon,
		BandwidthLimit: bandwidthBps,
		DefaultTheme:   defaultTheme,
	}, nil
}

// parseBandwidth converts a human-readable bandwidth string to bytes per
// second. Accepted units (case-insensitive): bps, kbps, mbps, gbps.
// A bare number is treated as bytes per second.
//
// Examples: "10mbps", "500 kbps", "1gbps", "131072"
func parseBandwidth(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	// Split into numeric prefix and unit suffix.
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("no numeric value found")
	}
	numStr := s[:i]
	unit := strings.ToLower(strings.TrimFunc(s[i:], unicode.IsSpace))

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || val < 0 {
		return 0, fmt.Errorf("invalid number %q", numStr)
	}

	// Convert bits/sec units to bytes/sec.
	switch unit {
	case "", "bps":
		return val / 8, nil
	case "kbps":
		return val * 1_000 / 8, nil
	case "mbps":
		return val * 1_000_000 / 8, nil
	case "gbps":
		return val * 1_000_000_000 / 8, nil
	default:
		return 0, fmt.Errorf("unknown unit %q (accepted: bps, kbps, mbps, gbps)", unit)
	}
}
