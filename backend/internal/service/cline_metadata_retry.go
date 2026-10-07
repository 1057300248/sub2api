package service

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type clineMetadataRetryError struct{ until time.Time }

func (e *clineMetadataRetryError) Error() string { return "Cline metadata temporarily unavailable" }

func clineMetadataRetryAt(header http.Header, now time.Time) time.Time {
	at := now.Add(30 * time.Second)
	value := strings.TrimSpace(header.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 && seconds <= 35*86400 {
		if next := now.Add(time.Duration(seconds) * time.Second); next.After(at) {
			at = next
		}
	} else if next, err := http.ParseTime(value); err == nil && next.After(at) && next.Sub(now) <= 35*24*time.Hour {
		at = next
	}
	return at
}
