// Package procmem reads the process resident set size from procfs.
//
// RSS is the number the kernel OOM-killer acts on, and on this workload it
// runs well above Go's runtime.MemStats.Sys because modernc.org/sqlite holds
// page cache and mmap pages outside the Go heap. Callers that plot memory
// against a VM limit should use RSS, not Sys.
package procmem

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// statmWarnOnce ensures the "no procfs" warning is emitted at most once.
var statmWarnOnce sync.Once

// ParseStatmRSS extracts the resident-pages field (the second whitespace
// separated value of /proc/self/statm) and converts it to bytes.
func ParseStatmRSS(content string, pageSize int) (int64, error) {
	fields := strings.Fields(content)
	if len(fields) < 2 {
		return 0, fmt.Errorf("statm: expected at least 2 fields, got %d", len(fields))
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("statm: parse resident pages: %w", err)
	}
	if pages < 0 {
		return 0, fmt.Errorf("statm: negative resident pages %d", pages)
	}
	return pages * int64(pageSize), nil
}

// RSSBytes returns the process resident set size in bytes. It returns 0 on
// platforms without procfs (macOS dev boxes), warning at most once.
func RSSBytes() int64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		statmWarnOnce.Do(func() {
			slog.Warn("rss sampling unavailable", "error", err)
		})
		return 0
	}
	rss, err := ParseStatmRSS(string(data), os.Getpagesize())
	if err != nil {
		statmWarnOnce.Do(func() {
			slog.Warn("rss sampling unavailable", "error", err)
		})
		return 0
	}
	return rss
}
