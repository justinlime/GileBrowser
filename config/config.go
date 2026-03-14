// Package config handles minimal server configuration via CLI flags.
// Most settings are now managed through the web interface and stored in SQLite.
package config

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// Config holds minimal server configuration from CLI flags.
// All other settings are loaded from the database at runtime.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port int
	// Host is the network interface to bind to (e.g., "0.0.0.0", "127.0.0.1").
	Host string
	// Dirs is the ordered list of root directories to serve.
	Dirs []string
	// StatsDir is where the SQLite database (gile.db) will be stored.
	StatsDir string
	// TrustedProxy is an optional IP/CIDR for reverse proxy setup.
	TrustedProxy string
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

// Load parses CLI flags and environment variables, returning a validated Config.
// Only essential runtime configuration is handled here; all UI and feature settings
// are now managed through the web interface.
func Load() (*Config, error) {
	var dirs dirList
	portFlag := flag.Int("port", 0, "HTTP port to listen on (env: GILE_PORT, default: 7887)")
	hostFlag := flag.String("host", "", "Network interface to bind to (env: GILE_HOST, default: 0.0.0.0)")
	statsDirFlag := flag.String("stats-dir", "", "Directory for gile.db SQLite database (env: GILE_STATS_DIR, default: current working directory)")
	trustedProxyFlag := flag.String("trusted-proxy", "", "IP or CIDR of trusted reverse proxy (env: GILE_TRUSTED_PROXY)")
	flag.Var(&dirs, "dir", "Root directory to serve (repeatable; env: GILE_DIRS, colon-separated)")
	flag.Parse()

	// --- port ---
	port := *portFlag
	if port == 0 {
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

	// --- host ---
	host := *hostFlag
	if host == "" {
        host = os.Getenv("GILE_HOST")
    }
	if host == "" {
        host = "0.0.0.0"
    }

	// --- dirs ---
	if len(dirs) == 0 {
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

	// --- stats-dir ---
	statsDir := *statsDirFlag
	if statsDir == "" {
        if v := os.Getenv("GILE_STATS_DIR"); v != "" {
            statsDir = v
        } else {
            cwd, err := os.Getwd()
            if err != nil {
                return nil, fmt.Errorf("could not determine current working directory: %w", err)
            }
            statsDir = cwd
        }
    }

	// --- trusted-proxy ---
	trustedProxy := *trustedProxyFlag
	if trustedProxy == "" {
        trustedProxy = os.Getenv("GILE_TRUSTED_PROXY")
    }

	// Validate directories.
	if len(dirs) == 0 {
        return nil, fmt.Errorf("at least one root directory must be specified via -dir flag, GILE_DIRS env var, or positional argument")
    }

	for _, d := range dirs {
        info, err := os.Stat(d)
        if err != nil {
            return nil, fmt.Errorf("directory %q: %w", d, err)
        }
        if !info.IsDir() {
            return nil, fmt.Errorf("%q is not a directory", d)
        }
    }

	log.Printf("config: loaded CLI flags - port=%d host=%s dirs=%v stats-dir=%s",
        port, host, dirs, statsDir)

	return &Config{
        Port:         port,
        Host:         host,
        Dirs:         []string(dirs),
        StatsDir:     statsDir,
        TrustedProxy: trustedProxy,
    }, nil
}
