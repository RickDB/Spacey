package database

import (
	"testing"
	"time"
)

func TestZFSRange(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		input, bucket string
		days          int
	}{{"24h", "15 minutes", 1}, {"7d", "1 hour", 7}, {"30d", "4 hours", 30}, {"90d", "12 hours", 90}, {"1y", "2 days", 365}}
	for _, tc := range tests {
		got, err := ZFSRange(tc.input, now)
		if err != nil || got.Bucket != tc.bucket || !got.Since.Equal(now.Add(-time.Duration(tc.days)*24*time.Hour)) {
			t.Fatalf("%s: %#v, %v", tc.input, got, err)
		}
	}
	if _, err := ZFSRange("all", now); err == nil {
		t.Fatal("invalid range accepted")
	}
}
