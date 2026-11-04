package docker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuardAllowed(t *testing.T) {
	id := "3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2e3d4c5b6a7f8e9d0c1b2a3f4e"
	allow := []string{
		"/_ping", "/v1.44/_ping", "/version", "/containers/json", "/containers/json?all=1",
		"/containers/" + id + "/stats?stream=false", "/events?filters=x",
	}
	deny := []struct{ method, path string }{
		{"GET", "/containers/" + id + "/json"}, // inspect leaks env vars
		{"POST", "/containers/" + id + "/stop"},
		{"POST", "/containers/create"},
		{"GET", "/containers/" + id + "/exec"},
		{"POST", "/exec/abc/start"},
		{"GET", "/images/json"},
		{"GET", "/volumes"},
		{"GET", "/containers/../images/json"},
		{"GET", "/containers/%2e%2e/images/json"},
		{"DELETE", "/containers/" + id},
		{"GET", "/containers/" + id + "/archive?path=/etc"},
		{"GET", "/containers/" + id + "/logs"},
	}
	for _, p := range allow {
		r := httptest.NewRequest("GET", "http://docker"+p, nil)
		if !GuardAllowed(r) {
			t.Errorf("should allow %s", p)
		}
	}
	for _, d := range deny {
		r, err := http.NewRequest(d.method, "http://docker"+d.path, nil)
		if err != nil {
			continue
		}
		if GuardAllowed(r) {
			t.Errorf("should deny %s %s", d.method, d.path)
		}
	}
}
