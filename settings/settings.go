// Package settings provides SQLite-based persistent configuration storage.
// All server settings are stored here and can be modified via the web interface.
package settings

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

var (
	dbPath string
	dbConn *sql.DB
)

// Settings holds all configurable server options.
type Settings struct {
	Title           string  `json:"title"`
	DefaultTheme    string  `json:"default_theme"`        // "dark" or "light"
	PreviewImages   bool    `json:"preview_images"`
	PreviewText     bool    `json:"preview_text"`
	PreviewDocs     bool    `json:"preview_docs"`
	BandwidthBps    float64 `json:"bandwidth_bps"`        // bytes per second, 0 = unlimited
	FaviconPath     string  `json:"favicon_path"`
	BandwidthString string  // Human-readable bandwidth for display (not persisted)
}

// DefaultSettings returns the initial values used when no settings exist yet.
func DefaultSettings() Settings {
	return Settings{
		Title:         "GileBrowser",
		DefaultTheme:  "dark",
		PreviewImages: true,
		PreviewText:   true,
		PreviewDocs:   true,
		BandwidthBps:  0,
		FaviconPath:   "",
	}
}

// InitSettings opens (or creates) the SQLite database at dbPath and ensures the settings table exists.
// It loads any existing settings or inserts defaults if this is a fresh installation.
func InitSettings(dbPath string) error {
	var err error
	dbConn, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("could not open database %s: %w", dbPath, err)
    }

	// Enable WAL mode for better concurrency and crash safety.
	if _, err := dbConn.Exec(`PRAGMA journal_mode=WAL`); err != nil {
        dbConn.Close()
		return fmt.Errorf("could not enable WAL mode: %w", err)
    }

	// Create the settings table if it doesn't exist.
	_, err = dbConn.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
                key   TEXT PRIMARY KEY,
                value TEXT NOT NULL
        )
	`)
	if err != nil {
		dbConn.Close()
		return fmt.Errorf("could not create settings table: %w", err)
    }

	// Initialize default settings if the table is empty.
	var count int
	err = dbConn.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&count)
	if err != nil {
        dbConn.Close()
		return fmt.Errorf("could not query settings count: %w", err)
    }

	if count == 0 {
        defaults := DefaultSettings()
		tx, err := dbConn.Begin()
		if err != nil {
            dbConn.Close()
            return fmt.Errorf("could not begin transaction: %w", err)
        }

        keyMap := map[string]string{
            "title":           defaults.Title,
            "default_theme":   defaults.DefaultTheme,
            "preview_images":  boolToString(defaults.PreviewImages),
            "preview_text":    boolToString(defaults.PreviewText),
            "preview_docs":    boolToString(defaults.PreviewDocs),
            "bandwidth_bps":   fmt.Sprintf("%g", defaults.BandwidthBps),
            "favicon_path":    defaults.FaviconPath,
        }

        for key, value := range keyMap {
            _, err = tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, key, value)
            if err != nil {
                tx.Rollback()
                dbConn.Close()
                return fmt.Errorf("could not insert default setting %s: %w", key, err)
            }
        }

        if err := tx.Commit(); err != nil {
            dbConn.Close()
            return fmt.Errorf("could not commit default settings: %w", err)
        }
    }

	dbPath = dbPath
	log.Printf("settings: initialized configuration database at %s", dbPath)
	return nil
}

// GetAllSettings retrieves all current settings from the database.
func GetAllSettings() (Settings, error) {
	defaults := DefaultSettings()

	rows, err := dbConn.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return defaults, fmt.Errorf("could not query settings: %w", err)
    }
	defer rows.Close()

	for rows.Next() {
        var key, value string
        if err := rows.Scan(&key, &value); err != nil {
            continue
        }

        switch key {
        case "title":
            defaults.Title = value
        case "default_theme":
            defaults.DefaultTheme = value
        case "preview_images":
            defaults.PreviewImages = stringToBool(value)
        case "preview_text":
            defaults.PreviewText = stringToBool(value)
        case "preview_docs":
            defaults.PreviewDocs = stringToBool(value)
        case "bandwidth_bps":
            fmt.Sscanf(value, "%f", &defaults.BandwidthBps)
        case "favicon_path":
            defaults.FaviconPath = value
        }
    }

	return defaults, nil
}

// SaveSetting updates a single setting value in the database.
func SaveSetting(key, value string) error {
	_, err := dbConn.Exec(`UPDATE settings SET value = ? WHERE key = ?`, value, key)
	if err != nil {
		return fmt.Errorf("could not update setting %s: %w", key, err)
    }
	log.Printf("settings: updated %s = %s", key, value)
	return nil
}

// SaveAllSettings updates all settings in a single transaction.
func SaveAllSettings(s Settings) error {
	tx, err := dbConn.Begin()
	if err != nil {
		return fmt.Errorf("could not begin transaction: %w", err)
    }

	keyMap := map[string]string{
        "title":           s.Title,
        "default_theme":   s.DefaultTheme,
        "preview_images":  boolToString(s.PreviewImages),
        "preview_text":    boolToString(s.PreviewText),
        "preview_docs":    boolToString(s.PreviewDocs),
        "bandwidth_bps":   fmt.Sprintf("%g", s.BandwidthBps),
        "favicon_path":    s.FaviconPath,
    }

	for key, value := range keyMap {
        _, err = tx.Exec(`UPDATE settings SET value = ? WHERE key = ?`, value, key)
        if err != nil {
            tx.Rollback()
            return fmt.Errorf("could not update setting %s: %w", key, err)
        }
    }

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not commit settings: %w", err)
    }

	log.Printf("settings: saved all configuration")
	return nil
}

// Helper functions for boolean serialization.
func boolToString(b bool) string {
	if b {
		return "true"
    }
	return "false"
}

func stringToBool(s string) bool {
	return s == "true" || s == "1"
}
