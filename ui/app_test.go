package main

import (
	"errors"
	"testing"
)

func TestNormalizeNetworkID(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "8056c2e21c000001", want: "8056c2e21c000001"},
		{in: "8056C2E21C000001", want: "8056c2e21c000001"},     // uppercase is lowered
		{in: "  8056c2e21c000001\n", want: "8056c2e21c000001"}, // surrounding space is dropped
		{in: "", wantErr: true},
		{in: "8056c2e21c00000", wantErr: true},   // 15 characters
		{in: "8056c2e21c0000011", wantErr: true}, // 17 characters
		{in: "8056c2e21c00000g", wantErr: true},  // not hex
		{in: "8056-c2e2-1c00-0001", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeNetworkID(tt.in)
		if tt.wantErr {
			// errors.Is checks whether err is (or wraps) errBadNetworkID.
			if !errors.Is(err, errBadNetworkID) {
				t.Errorf("normalizeNetworkID(%q) error = %v, want errBadNetworkID", tt.in, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("normalizeNetworkID(%q) = %q, %v; want %q, nil", tt.in, got, err, tt.want)
		}
	}
}
