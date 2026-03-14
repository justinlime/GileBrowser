// Package handlers provides HTTP request handlers for the GileBrowser server.
// This file implements the settings management interface.
package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"gileserver/settings"
)

// SettingsPageData holds data for rendering the settings page.
type SettingsPageData struct {
	Title          string
	SiteName       string
	Settings       settings.Settings
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
	if err := r.ParseForm(); err != nil {
        http.Error(w, "Unable to parse form", http.StatusBadRequest)
        return
    }

	newSettings := settings.Settings{
        Title:       r.FormValue("title"),
        DefaultTheme: r.FormValue("default_theme"),
        FaviconPath:  r.FormValue("favicon_path"),
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

	// Save settings to database.
	if err := SaveSettings(newSettings); err != nil {
        log.Printf("settings: failed to save: %v", err)
        http.Error(w, "Failed to save settings", http.StatusInternalServerError)
        return
    }

	log.Printf("settings: updated by user - title=%q theme=%q previews=img=%v txt=%v doc=%v bw=%.0f bps",
        newSettings.Title, newSettings.DefaultTheme,
        newSettings.PreviewImages, newSettings.PreviewText, newSettings.PreviewDocs,
        newSettings.BandwidthBps*8)

	// Redirect back to settings page with success message.
	http.Redirect(w, r, "/settings?success=1", http.StatusSeeOther)
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
