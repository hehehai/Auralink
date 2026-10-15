package main

// Small formatting helpers the pages share.

import (
	"fmt"
	"os"
	"path/filepath"
)

func itoa(n int) string { return fmt.Sprint(n) }

// humanBytes shows a size: "1.2 GB", "340 MB", "12 KB", "512 B".
func humanBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f KB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

// shortPath shows a path under the home folder as "~/…".
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !filepath.IsAbs(rel) && rel != ".." && len(rel) < len(p) && rel[:min(2, len(rel))] != ".." {
			return "~/" + rel
		}
	}
	return p
}

// orDash shows "—" for an empty value.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
