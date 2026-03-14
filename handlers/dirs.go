// Package handlers provides HTTP request handlers for the GileBrowser server.
// This file implements root directory management via REST API.
package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gileserver/settings"
)

// DirAPIResponse is the response structure for directory API endpoints.
type DirAPIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Error   string      `json:"error,omitempty"`
	Dirs    []DirInfo   `json:"dirs,omitempty"`
}

// DirInfo contains information about a root directory.
type DirInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ListDirsHandler returns JSON list of all configured root directories.
func ListDirsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		roots := GetAllRootDirs()

		var dirInfos []DirInfo
		for _, rd := range roots {
			dirInfos = append(dirInfos, DirInfo{
				Name: rd.Name,
				Path: rd.Path,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DirAPIResponse{
			Success: true,
			Dirs:    dirInfos,
		})
	}
}

// AddDirHandler adds a new root directory from a form submission.
// Expects multipart/form-data with a "directory" field containing the path.
func AddDirHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse form (support both regular POST and file uploads for path selection)
		if err := r.ParseMultipartForm(0); err != nil {
			log.Printf("handlers: failed to parse form: %v", err)
			http.Error(w, "Unable to parse form", http.StatusBadRequest)
			return
		}

		// Get directory path from form field or file upload
		var dirPath string
		
		// Check for file upload first (from directory picker)
		if file, _, err := r.FormFile("directory"); err == nil {
			defer file.Close()
			// Read the path from the uploaded "file" (it's actually a path pointer)
			buf := make([]byte, 4096)
			n, _ := file.Read(buf)
			dirPath = strings.TrimSpace(string(buf[:n]))
		} else {
			// Fall back to form field
			dirPath = strings.TrimSpace(r.FormValue("directory_path"))
		}

		if dirPath == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DirAPIResponse{
				Success: false,
				Error:   "Directory path is required",
			})
			return
		}

		// Clean and validate the path.
		dirPath = filepath.Clean(dirPath)
		info, err := os.Stat(dirPath)
		if err != nil {
			log.Printf("handlers: directory %q does not exist: %v", dirPath, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DirAPIResponse{
				Success: false,
				Error:   fmt.Sprintf("Directory does not exist: %v", err),
			})
			return
		}
		if !info.IsDir() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DirAPIResponse{
				Success: false,
				Error:   fmt.Sprintf("%q is not a directory", dirPath),
			})
			return
		}

		// Generate URL-safe name.
		name := rootName(dirPath)

		// Check if this path already exists under any name.
		existingRoots := GetAllRootDirs()
		for _, existing := range existingRoots {
			if existing.Path == dirPath {
				log.Printf("handlers: directory %q already configured as %q", dirPath, existing.Name)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(DirAPIResponse{
					Success: false,
					Error:   fmt.Sprintf("Directory already configured as %q", existing.Name),
				})
				return
			}
		}

		// Add the new root directory.
		if err := AddRootDir(name, dirPath); err != nil {
			log.Printf("handlers: failed to add root directory: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DirAPIResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to add directory: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DirAPIResponse{
			Success: true,
			Message: fmt.Sprintf("Added directory %q as %q", dirPath, name),
		})
	}
}

// RemoveDirHandler removes a root directory by name.
func RemoveDirHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.ParseForm()
		name := r.FormValue("name")

		if name == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DirAPIResponse{
				Success: false,
				Error:   "Directory name is required",
			})
			return
		}

		if err := RemoveRootDir(name); err != nil {
			log.Printf("handlers: failed to remove root directory: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DirAPIResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to remove directory: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DirAPIResponse{
			Success: true,
			Message: fmt.Sprintf("Removed directory %q", name),
		})
	}
}

// GetAllRootDirs returns all root directories from the runtime config.
func GetAllRootDirs() []settings.RootDir {
	rtc := GetRuntimeConfig()
	return rtc.RootDirs
}
