package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRateLimitDefaultsAndOverrides(t *testing.T) {
	previous := Get()
	defer Set(previous)
	for _, test := range []struct {
		name  string
		body  string
		rate  float64
		burst int
	}{
		{name: "generated configuration", rate: 100, burst: 200},
		{name: "omitted limits", body: `{}`, rate: 100, burst: 200},
		{name: "explicit legacy limits", body: `{"rate_limit_rps":10,"rate_limit_burst":20}`, rate: 10, burst: 20},
		{name: "custom limits", body: `{"rate_limit_rps":250,"rate_limit_burst":500}`, rate: 250, burst: 500},
		{name: "disabled limiter", body: `{"rate_limit_rps":0,"rate_limit_burst":0}`, rate: 0, burst: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "settings.json")
			if test.body != "" {
				if err := os.WriteFile(filename, []byte(test.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := Load(filename); err != nil {
				t.Fatal(err)
			}
			loaded := Get()
			if loaded.RateLimitRPS != test.rate || loaded.RateLimitBurst != test.burst {
				t.Fatalf("limits = %v/%d, want %v/%d", loaded.RateLimitRPS, loaded.RateLimitBurst, test.rate, test.burst)
			}
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if test.body != "" {
				if string(data) != test.body {
					t.Fatal("existing settings were overwritten")
				}
			} else {
				var saved Settings
				if err := json.Unmarshal(data, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.RateLimitRPS != test.rate || saved.RateLimitBurst != test.burst {
					t.Fatal("generated settings did not persist the new defaults")
				}
			}
		})
	}
}
