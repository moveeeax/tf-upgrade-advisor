package outdated

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultRegistry is the public Terraform Registry.
const DefaultRegistry = "https://registry.terraform.io"

// errNotFound is returned when the registry has no such module or provider.
var errNotFound = errors.New("not found in the registry")

// Release is one published version.
type Release struct {
	Version string
	// Deprecation is the registry's deprecation notice for this version, empty
	// when there is none.
	Deprecation string
}

// Registry reads published versions from a Terraform Registry. Only the two
// version-list endpoints are used; nothing is downloaded.
type Registry struct {
	BaseURL string
	HTTP    *http.Client

	modules   map[string][]Release
	providers map[string]providerVersions
}

type providerVersions struct {
	releases []Release
	warnings []string
}

// NewRegistry returns a client for the registry at baseURL.
func NewRegistry(baseURL string) *Registry {
	return &Registry{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		modules:   map[string][]Release{},
		providers: map[string]providerVersions{},
	}
}

// ModuleVersions lists every published version of namespace/name/system.
//
// GET /v1/modules/:namespace/:name/:system/versions. Each version may carry a
// `deprecation` object; any non-null value marks that version deprecated.
func (r *Registry) ModuleVersions(ctx context.Context, address string) ([]Release, error) {
	if cached, ok := r.modules[address]; ok {
		return cached, nil
	}
	var body struct {
		Modules []struct {
			Versions []struct {
				Version     string          `json:"version"`
				Deprecation json.RawMessage `json:"deprecation"`
			} `json:"versions"`
		} `json:"modules"`
	}
	if err := r.get(ctx, "/v1/modules/"+address+"/versions", &body); err != nil {
		return nil, fmt.Errorf("module %s: %w", address, err)
	}
	var out []Release
	for _, m := range body.Modules {
		for _, v := range m.Versions {
			out = append(out, Release{Version: v.Version, Deprecation: deprecationNote(v.Deprecation)})
		}
	}
	r.modules[address] = out
	return out, nil
}

// ProviderVersions lists every published version of namespace/type plus the
// provider-level warnings the registry attaches (Terraform prints these on
// `init`; the registry uses them for deprecated and moved providers).
//
// GET /v1/providers/:namespace/:type/versions.
func (r *Registry) ProviderVersions(ctx context.Context, address string) ([]Release, []string, error) {
	if cached, ok := r.providers[address]; ok {
		return cached.releases, cached.warnings, nil
	}
	var body struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
		Warnings []string `json:"warnings"`
	}
	if err := r.get(ctx, "/v1/providers/"+address+"/versions", &body); err != nil {
		return nil, nil, fmt.Errorf("provider %s: %w", address, err)
	}
	out := make([]Release, 0, len(body.Versions))
	for _, v := range body.Versions {
		out = append(out, Release{Version: v.Version})
	}
	r.providers[address] = providerVersions{releases: out, warnings: body.Warnings}
	return out, body.Warnings, nil
}

func (r *Registry) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tf-upgrade-advisor")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("registry returned %s: %s", resp.Status, bytes.TrimSpace(snippet))
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// deprecationNote turns the registry's per-version `deprecation` value into a
// line for the report. The public registry returns null for live versions; the
// shape of a populated value is not part of a published protocol, so a
// `reason` and `link` are used when present and anything else still counts.
func deprecationNote(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	switch string(trimmed) {
	case "", "null", "false", "{}":
		return ""
	}
	var obj struct {
		Reason string `json:"reason"`
		Link   string `json:"link"`
	}
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		switch {
		case obj.Reason != "" && obj.Link != "":
			return obj.Reason + " (" + obj.Link + ")"
		case obj.Reason != "":
			return obj.Reason
		case obj.Link != "":
			return obj.Link
		}
	}
	return "deprecated in the registry"
}
