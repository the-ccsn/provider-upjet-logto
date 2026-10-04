package main

import (
	"testing"
	"time"
)

func TestRuntimeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name                string
		sync, poll, metrics time.Duration
		rate, port          int
		invalid             bool
	}{
		{"valid", time.Hour, time.Minute, time.Second, 10, 9443, false},
		{"zero-sync", 0, time.Minute, time.Second, 10, 9443, true},
		{"negative-poll", time.Hour, -time.Second, time.Second, 10, 9443, true},
		{"zero-metrics", time.Hour, time.Minute, 0, 10, 9443, true},
		{"zero-rate", time.Hour, time.Minute, time.Second, 0, 9443, true},
		{"invalid-port", time.Hour, time.Minute, time.Second, 10, 65536, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRuntimeOptions(tc.sync, tc.poll, tc.metrics, tc.rate, tc.port); (err != nil) != tc.invalid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}
