package outdated

import (
	"regexp"
	"strings"
)

// publicHost is the registry host implied by a source with no hostname.
const publicHost = "registry.terraform.io"

// Registry address components, as Terraform validates them: namespace and name
// are up to 64 alphanumerics with inner dashes and underscores, the target
// system is lowercase alphanumerics only.
var (
	namePart   = regexp.MustCompile(`^[0-9A-Za-z](?:[0-9A-Za-z_-]{0,62}[0-9A-Za-z])?$`)
	systemPart = regexp.MustCompile(`^[0-9a-z]{1,64}$`)
)

// moduleAddress parses a module `source` as a registry address and returns
// the host and `namespace/name/system`. ok is false for anything that is not a
// registry source: local paths, git, http, S3, GCS, and the github.com and
// bitbucket.org shorthands Terraform resolves before it ever asks a registry.
func moduleAddress(source string) (host, address string, ok bool) {
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, "/") || strings.Contains(source, "::") ||
		strings.Contains(source, "://") || strings.Contains(source, "?") {
		return "", "", false
	}
	// `namespace/name/system//modules/sub` selects a subdirectory of the same
	// package, which shares the package's versions.
	if i := strings.Index(source, "//"); i >= 0 {
		source = source[:i]
	}
	parts := strings.Split(source, "/")
	host = publicHost
	switch len(parts) {
	case 3:
	case 4:
		host = strings.ToLower(parts[0])
		parts = parts[1:]
	default:
		return "", "", false
	}
	if host == "github.com" || host == "bitbucket.org" || strings.Contains(parts[0], ".") {
		return "", "", false
	}
	if !namePart.MatchString(parts[0]) || !namePart.MatchString(parts[1]) || !systemPart.MatchString(parts[2]) {
		return "", "", false
	}
	return host, strings.Join(parts, "/"), true
}

// providerAddress normalizes a `required_providers` entry to its host and
// `namespace/type`. A sourceless legacy entry means hashicorp/<name>, as it
// does in Terraform itself.
func providerAddress(name, source string) (host, address string, ok bool) {
	if source == "" {
		return publicHost, "hashicorp/" + strings.ToLower(name), true
	}
	parts := strings.Split(strings.ToLower(source), "/")
	host = publicHost
	switch len(parts) {
	case 2:
	case 3:
		host = parts[0]
		parts = parts[1:]
	default:
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return host, parts[0] + "/" + parts[1], true
}
