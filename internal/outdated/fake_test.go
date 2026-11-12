package outdated

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeModules and fakeProviders are what the fake registry publishes for the
// testdata/outdated fixture. Shapes follow the live responses of
// registry.terraform.io/v1/modules/.../versions and /v1/providers/.../versions.
var fakeModules = map[string][]map[string]any{
	"terraform-aws-modules/vpc/aws": {
		{"version": "5.8.1", "deprecation": nil},
		{"version": "5.9.0", "deprecation": nil},
		{"version": "6.0.0", "deprecation": nil},
		{"version": "6.1.0-beta1", "deprecation": nil},
	},
	"terraform-aws-modules/security-group/aws": {
		{"version": "5.1.0"}, {"version": "5.1.2"}, {"version": "5.2.0"},
	},
	"example/gone/aws": {
		{"version": "1.0.0"}, {"version": "1.2.4"},
	},
	"terraform-aws-modules/s3-bucket/aws": {
		{"version": "4.1.0"}, {"version": "5.0.0"},
	},
	"terraform-aws-modules/iam/aws": {
		{"version": "5.0.0", "deprecation": map[string]string{
			"reason": "Use terraform-aws-modules/iam-role/aws.",
			"link":   "https://example.com/iam-deprecation",
		}},
	},
}

var fakeProviders = map[string]map[string]any{
	"hashicorp/aws": {"versions": []map[string]string{
		{"version": "5.40.0"}, {"version": "5.100.0"}, {"version": "6.0.0"}, {"version": "6.2.0"},
	}},
	"hashicorp/random": {"versions": []map[string]string{
		{"version": "3.6.0"}, {"version": "3.6.3"},
	}},
	"hashicorp/template": {
		"versions": []map[string]string{{"version": "2.2.0"}},
		"warnings": []string{"This provider is archived and no longer maintained."},
	},
}

// fakeRegistry serves fakeModules and fakeProviders and records the paths it
// was asked for.
type fakeRegistry struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()

		addr, ok := strings.CutSuffix(r.URL.Path, "/versions")
		if !ok {
			http.NotFound(w, r)
			return
		}
		var body any
		if m, ok := strings.CutPrefix(addr, "/v1/modules/"); ok {
			if versions, found := fakeModules[m]; found {
				body = map[string]any{"modules": []map[string]any{{"source": m, "versions": versions}}}
			}
		} else if p, ok := strings.CutPrefix(addr, "/v1/providers/"); ok {
			if v, found := fakeProviders[p]; found {
				body = v
			}
		}
		if body == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRegistry) requests(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if p == path {
			n++
		}
	}
	return n
}
