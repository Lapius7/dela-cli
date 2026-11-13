package main

import "testing"

func TestNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://app-abc123xyz0." + tunnelHost:    "app",
		"https://my-api-abc123xyz0." + tunnelHost: "my-api",
		"https://test-abc123xyz0." + tunnelHost:   "test",
	}
	for in, want := range cases {
		if got := nameFromURL(in); got != want {
			t.Errorf("nameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToTunnelJSONFallsBackToURLName(t *testing.T) {
	r := tunnelRecord{PID: 1, URL: "https://api-abc123xyz0." + tunnelHost, Target: "localhost:3000"}
	if got := toTunnelJSON(r).Name; got != "api" {
		t.Errorf("name = %q, want api", got)
	}
	r.Name = "web"
	if got := toTunnelJSON(r).Name; got != "web" {
		t.Errorf("name = %q, want web", got)
	}
}
