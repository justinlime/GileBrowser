// Package handlers provides HTTP request handlers for the GileBrowser server.
// This file implements the settings management interface.
package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gileserver/settings"
)

// SettingsPageData holds data for rendering the settings page.
type SettingsPageData struct {
	Title          string
	SiteName       string
	Settings       settings.Settings
	RootDirs       []settings.RootDir  // List of configured root directories
	SuccessMessage string
	ErrorMessage   string
	DefaultTheme   string
}

// SettingsHandler returns an http.HandlerFunc that serves the settings page (GET)
// and handles form submissions (POST).
func SettingsHandler(tmpl interface{ ExecuteSettings(http.ResponseWriter, interface{}) error }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
        rtc := GetRuntimeConfig()

        switch r.Method {
        case http.MethodGet:
            handleSettingsGet(w, r, rtc, tmpl)
        case http.MethodPost:
            handleSettingsPost(w, r, rtc)
        default:
            http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        }
    }
}

// handleSettingsGet renders the settings page with current values.
func handleSettingsGet(w http.ResponseWriter, r *http.Request, rtc RuntimeConfig, tmpl interface{ ExecuteSettings(http.ResponseWriter, interface{}) error }) {
	current := settings.Settings{
        Title:         rtc.Title,
        DefaultTheme:  rtc.DefaultTheme,
        PreviewImages: rtc.PreviewImages,
        PreviewText:   rtc.PreviewText,
        PreviewDocs:   rtc.PreviewDocs,
        BandwidthBps:  rtc.BandwidthBps,
        FaviconPath:   rtc.FaviconPath,
    }

	// Convert bandwidth to human-readable string for display.
	bandwidthStr := ""
	if current.BandwidthBps > 0 {
        bits := current.BandwidthBps * 8
        if bits >= 1_000_000_000 {
            bandwidthStr = fmt.Sprintf("%.0fgbps", bits/1_000_000_000)
        } else if bits >= 1_000_000 {
            bandwidthStr = fmt.Sprintf("%.0fmbps", bits/1_000_000)
        } else if bits >= 1_000 {
            bandwidthStr = fmt.Sprintf("%.0fkbps", bits/1_000)
        } else {
            bandwidthStr = fmt.Sprintf("%dbps", int(bits))
        }
    }

	current.BandwidthString = bandwidthStr

	// Check for success message from redirect.
	successMsg := ""
	if r.URL.Query().Get("success") == "1" {
        successMsg = "Settings saved successfully. Changes take effect immediately."}

	data := SettingsPageData{
        Title:          "Settings",
        SiteName:       rtc.Title,
        Settings:       current,
		RootDirs:         rtc.RootDirs,
        SuccessMessage: successMsg,
        ErrorMessage:   "",
        DefaultTheme:   rtc.DefaultTheme,
    }

	// Execute the pre-loaded settings template.
	if err := tmpl.ExecuteSettings(w, data); err != nil {
        log.Printf("settings: template execution failed: %v", err)
        http.Error(w, "Internal server error", http.StatusInternalServerError)
    }
}

// handleSettingsPost processes form submission and saves new settings.
func handleSettingsPost(w http.ResponseWriter, r *http.Request, rtc RuntimeConfig) {
	// Parse multipart form (supports both regular POST and file uploads)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
        log.Printf("settings: failed to parse form: %v", err)
        http.Error(w, "Unable to parse form", http.StatusBadRequest)
        return
    }

	newSettings := settings.Settings{
        Title:       r.FormValue("title"),
        DefaultTheme: r.FormValue("default_theme"),
        FaviconPath:  rtc.FaviconPath, // Start with current value
    }

	// Validate title.
	if strings.TrimSpace(newSettings.Title) == "" {
        newSettings.Title = rtc.Title // Keep existing if empty.
    } else {
        newSettings.Title = strings.TrimSpace(newSettings.Title)
    }

	// Validate theme.
	if newSettings.DefaultTheme != "dark" && newSettings.DefaultTheme != "light" {
        newSettings.DefaultTheme = rtc.DefaultTheme // Keep existing if invalid.
    }

	// Parse boolean fields.
	newSettings.PreviewImages = r.FormValue("preview_images") == "on"
	newSettings.PreviewText = r.FormValue("preview_text") == "on"
	newSettings.PreviewDocs = r.FormValue("preview_docs") == "on"

	// Parse bandwidth limit.
	bwStr := strings.TrimSpace(r.FormValue("bandwidth_bps"))
	if bwStr != "" {
        bps, err := parseBandwidthFromString(bwStr)
        if err != nil {
            log.Printf("settings: invalid bandwidth %q: %v", bwStr, err)
            // Keep existing value on parse error.
            newSettings.BandwidthBps = rtc.BandwidthBps
        } else {
            newSettings.BandwidthBps = bps
        }
    } else {
        newSettings.BandwidthBps = 0 // Empty means unlimited.
    }

	// Handle favicon deletion (user clicked × button)
	if r.FormValue("delete_favicon") == "1" && rtc.FaviconPath != "" {
        log.Printf("settings: deleting custom favicon %s", rtc.FaviconPath)
        if err := os.Remove(rtc.FaviconPath); err != nil {
            log.Printf("settings: failed to delete favicon %s: %v", rtc.FaviconPath, err)
        }
        newSettings.FaviconPath = ""
    }

	// Handle favicon upload
	if file, header, err := r.FormFile("favicon"); err == nil && header.Size > 0 {
        log.Printf("settings: processing favicon upload - %s (%d bytes)", header.Filename, header.Size)
        defer file.Close()
        
        // Sanitize filename.
        filename := sanitizeFaviconFilename(header.Filename)
        if filename == "" {
            log.Printf("settings: invalid favicon filename")
            http.Error(w, "Invalid filename", http.StatusBadRequest)
            return
        }

        dataDir := GetDataDir()
        faviconPath := filepath.Join(dataDir, "favicons", filename)

        // Ensure the favicons directory exists.
        if err := os.MkdirAll(filepath.Dir(faviconPath), 0755); err != nil {
            log.Printf("settings: failed to create favicon directory: %v", err)
            http.Error(w, "Failed to create upload directory", http.StatusInternalServerError)
            return
        }

        // Save the uploaded file.
        out, err := os.Create(faviconPath)
        if err != nil {
            log.Printf("settings: failed to create favicon file: %v", err)
            http.Error(w, "Failed to save upload", http.StatusInternalServerError)
            return
        }
        
        if _, err := io.Copy(out, file); err != nil {
            out.Close()
            log.Printf("settings: failed to write favicon: %v", err)
            http.Error(w, "Failed to save upload", http.StatusInternalServerError)
            return
        }
        out.Close()

        newSettings.FaviconPath = faviconPath
        					log.Printf("settings: favicon uploaded as %s", filename)
        		            }
        		        
        				// Process directory deletions
        			deletedDirsStr := r.FormValue("deleted_dirs")
        			if deletedDirsStr != "" {
        				deletedDirs := strings.Split(deletedDirsStr, ",")
        				for _, name := range deletedDirs {
        					name = strings.TrimSpace(name)
        					if name != "" {
        						log.Printf("settings: removing directory %q", name)
        						if err := settings.RemoveRoot(name); err != nil {
        							log.Printf("settings: failed to remove directory %q: %v", name, err)
        						}
        					}
        				}
        			}
        		
        			// Process new directory additions
        			i := 0
        			for {
        				nameKey := fmt.Sprintf("new_dir_name_%d", i)
        				pathKey := fmt.Sprintf("new_dir_path_%d", i)
        				
        				name := strings.TrimSpace(r.FormValue(nameKey))
        				path := strings.TrimSpace(r.FormValue(pathKey))
        				
        				if name == "" || path == "" {
        					break // No more new directories
        				}
        				
        				// Validate the path exists and is a directory
        				info, err := os.Stat(path)
        				if err != nil {
        					log.Printf("settings: skipping invalid directory path %q: %v", path, err)
        					i++
        					continue
        				}
        				if !info.IsDir() {
        					log.Printf("settings: skipping %q - not a directory", path)
        					i++
        					continue
        				}
        				
        				log.Printf("settings: adding directory %q -> %q", name, path)
        				if err := settings.AddRoot(name, path); err != nil {
        					log.Printf("settings: failed to add directory %q: %v", name, err)
        				}
        				i++
        			}
        		
        			// Process directory renames (updated names in existing directories)
        			for _, rd := range rtc.RootDirs {
        				nameKey := "dir_name_" + rd.Name
        				newName := strings.TrimSpace(r.FormValue(nameKey))
        				
        				if newName == "" || newName == rd.Name {
        					continue // No change
        				}
        				
        				// Sanitize the new name
        				safeName := strings.ToLower(newName)
        				safeName = strings.ReplaceAll(safeName, " ", "-")
        				safeName = strings.TrimFunc(safeName, func(r rune) bool {
        					return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-')
        				})
        				
        				if safeName != rd.Name && safeName != "" {
        					// Remove old entry and add with new name
        					log.Printf("settings: renaming directory %q to %q", rd.Name, safeName)
        					settings.RemoveRoot(rd.Name)
        					settings.AddRoot(safeName, rd.Path)
        				}
        			}
        		        
        			// Save settings to database.
	if err := SaveSettings(newSettings); err != nil {
        log.Printf("settings: failed to save: %v", err)
        http.Error(w, "Failed to save settings", http.StatusInternalServerError)
        return
    }

	// Refresh roots state if directories were changed.
	if deletedDirsStr != "" || i > 0 { // deletions or additions occurred
		RefreshRootsState()
	}

	log.Printf("settings: updated by user - title=%q theme=%q previews=img=%v txt=%v doc=%v bw=%.0f bps",
        newSettings.Title, newSettings.DefaultTheme,
        newSettings.PreviewImages, newSettings.PreviewText, newSettings.PreviewDocs,
        newSettings.BandwidthBps*8)

	// Return JSON response with success message for AJAX handling.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"success": true, "message": "Settings Saved"}`)
}

// parseBandwidthFromString converts a human-readable bandwidth string to bytes per second.
// Accepted formats: "10mbps", "500kbps", "1gbps", "131072" (bare number = bps).
func parseBandwidthFromString(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
        return 0, nil
    }

	// Find where the numeric part ends.
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
        i++
    }
	if i == 0 {
        return 0, fmt.Errorf("no numeric value found")
    }

	numStr := s[:i]
	unit := strings.ToLower(strings.TrimSpace(s[i:]))

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
