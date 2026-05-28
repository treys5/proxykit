package main

// ── App data export / import ──────────────────────────────────────────────────
// Lets users bundle all local config/state into a ZIP for device migration.
// device_id.json is intentionally excluded — each device must have its own ID.
// After importing on the new device the user should sign in fresh (or transfer).

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// exportFiles is the ordered list of JSON files included in an export bundle.
var exportFiles = []string{
	"cloud_auth.json",
	"score-config.json",
	"analytics.json",
	"notify.json",
	"calibration.json",
	"schedules.json",
	"px_status.json",
	"ai-keys.json",
	"proxy_lists.json",
	"integrations.json",
}

// handleExport streams a ZIP of all exportable app-data files as a download.
func handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		errJSON(w, 405, "method not allowed")
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, name := range exportFiles {
		fp := filepath.Join(DataDir, name)
		data, err := os.ReadFile(fp)
		if err != nil {
			continue // file may not exist yet — skip silently
		}
		f, err := zw.Create(name)
		if err != nil {
			continue
		}
		f.Write(data)
	}

	if err := zw.Close(); err != nil {
		errJSON(w, 500, "zip error")
		return
	}

	ts := time.Now().Format("20060102-150405")
	fname := fmt.Sprintf("proxykit-data-%s.zip", ts)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	w.Write(buf.Bytes())
}

// handleImport restores app data from a previously exported ZIP bundle.
// Only files in the exportFiles allowlist are written; device_id.json is
// never touched regardless of bundle contents.
func handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}

	// ZIP arrives as multipart/form-data field "file"
	fields, err := parseMultipartForm(r)
	if err != nil {
		errJSON(w, 400, "expected multipart/form-data with a 'file' field: "+err.Error())
		return
	}
	zipData, ok := fields["file"]
	if !ok || len(zipData) == 0 {
		errJSON(w, 400, "no file field")
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		errJSON(w, 400, "invalid zip: "+err.Error())
		return
	}

	// Build allowlist — never include device_id.json
	allowed := make(map[string]bool, len(exportFiles))
	for _, name := range exportFiles {
		allowed[name] = true
	}

	var restored, skipped []string
	for _, f := range zr.File {
		name := f.Name
		if !allowed[name] {
			skipped = append(skipped, name)
			continue
		}
		rc, err := f.Open()
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		// Validate JSON before writing — reject malformed files
		var check any
		if err := json.Unmarshal(data, &check); err != nil {
			skipped = append(skipped, name+" (invalid JSON)")
			continue
		}

		fp := filepath.Join(DataDir, name)
		if err := os.WriteFile(fp, data, 0644); err != nil {
			skipped = append(skipped, name+" (write error)")
			continue
		}
		restored = append(restored, name)
	}

	// Reload in-memory state for files that have live caches
	for _, name := range restored {
		switch name {
		case "cloud_auth.json":
			loadCloudAuth()
		case "schedules.json":
			loadSchedules()
		case "px_status.json":
			loadPXStatuses()
		}
	}

	if restored == nil {
		restored = []string{}
	}
	if skipped == nil {
		skipped = []string{}
	}

	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"restored": restored,
		"skipped":  skipped,
	})
}
