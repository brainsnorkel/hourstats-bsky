package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "true") || v == "1"
}

// envList reads a comma-separated list, trimming whitespace and dropping
// empty entries. An unset or all-empty value returns nil.
func envList(key string) []string {
	return splitList(os.Getenv(key))
}

// splitList parses a comma-separated list, trimming whitespace and dropping
// empty entries. It is shared by envList and the lists read from key_value, so
// a value hand-edited over `fly ssh` behaves like its environment equivalent.
func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s=%q, using default %d\n", key, v, fallback)
		return fallback
	}
	return n
}
