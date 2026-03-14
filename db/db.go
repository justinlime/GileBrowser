// Package db provides SQLite-based persistent storage for download statistics.
// Uses atomic transactions to ensure data durability on every write.
package db

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

// InitDB opens (or creates) the SQLite database at dbPath and ensures the stats table exists.
// Call this once during server startup before any stats operations.
func InitDB(dbPath string) error {
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

	// Create the stats table if it doesn't exist, initializing counters to zero.
	_, err = dbConn.Exec(`
		CREATE TABLE IF NOT EXISTS stats (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			total_downloads INTEGER NOT NULL DEFAULT 0,
			total_bytes     INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		dbConn.Close()
		return fmt.Errorf("could not create stats table: %w", err)
	}

	// Ensure the single row exists with zeroed counters if the table was just created.
	_, err = dbConn.Exec(`INSERT OR IGNORE INTO stats (id, total_downloads, total_bytes) VALUES (1, 0, 0)`)
	if err != nil {
		dbConn.Close()
		return fmt.Errorf("could not initialize stats row: %w", err)
	}

	dbPath = dbPath
	log.Printf("db: initialized SQLite database at %s", dbPath)
	return nil
}

// RecordDownload atomically increments the download counters by one download of bytesSent bytes.
// The transaction is committed immediately to ensure durability; errors are logged but not returned
// to avoid blocking the HTTP response path.
func RecordDownload(bytesSent int64) {
	tx, err := dbConn.Begin()
	if err != nil {
		log.Printf("db: could not begin transaction for download record: %v", err)
		return
	}

	_, err = tx.Exec(`UPDATE stats SET total_downloads = total_downloads + 1, total_bytes = total_bytes + ? WHERE id = 1`, bytesSent)
	if err != nil {
		tx.Rollback()
		log.Printf("db: could not update stats: %v", err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("db: could not commit download record: %v", err)
	}
}

// GetStats returns the current total downloads and total bytes served.
func GetStats() (totalDownloads, totalBytes int64, err error) {
	err = dbConn.QueryRow(`SELECT total_downloads, total_bytes FROM stats WHERE id = 1`).Scan(&totalDownloads, &totalBytes)
	if err != nil {
		return 0, 0, fmt.Errorf("could not read stats: %w", err)
	}
	return totalDownloads, totalBytes, nil
}
