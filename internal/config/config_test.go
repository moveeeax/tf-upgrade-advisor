package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, dir string) *Config {
	t.Helper()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	return cfg
}

func usage(t *testing.T, cfg *Config, address string) Usage {
	t.Helper()
	for _, u := range cfg.Usages {
		if u.Address == address {
			return u
		}
	}
	t.Fatalf("no block with address %q; have %d blocks", address, len(cfg.Usages))
	return Usage{}
}

func hasPath(u Usage, path string) bool {
	for _, a := range u.Attrs {
		if a.Path == path {
			return true
		}
	}
	return false
}

func TestLoadCollectsBlocksAndAddresses(t *testing.T) {
	cfg := load(t, "../../testdata/aws5")

	for _, want := range []string{
		"aws_eip.nat",
		"aws_flow_log.vpc",
		"aws_instance.app",
		"provider.aws",
		"data.aws_ami.amazon_linux", // reached through the local module
		"aws_ssm_association.patch",
	} {
		usage(t, cfg, want) // fails the test if missing
	}

	eip := usage(t, cfg, "aws_eip.nat")
	if eip.Kind != KindResource || eip.Type != "aws_eip" || eip.Name != "nat" {
		t.Fatalf("unexpected block metadata: %+v", eip)
	}
	if eip.Line != 20 {
		t.Errorf("aws_eip.nat line = %d, want 20", eip.Line)
	}
	if !strings.HasSuffix(eip.File, filepath.Join("aws5", "main.tf")) {
		t.Errorf("aws_eip.nat file = %s, want .../aws5/main.tf", eip.File)
	}

	ami := usage(t, cfg, "data.aws_ami.amazon_linux")
	if ami.Kind != KindData {
		t.Errorf("data source kind = %q, want %q", ami.Kind, KindData)
	}
}

func TestFlattenNestedAndDynamicBlocks(t *testing.T) {
	cfg := load(t, "../../testdata/aws5")

	lt := usage(t, cfg, "aws_launch_template.workers")
	// A dynamic block must flatten to the block type it generates, so the same
	// rule matches static and dynamic configuration alike.
	if !hasPath(lt, "block_device_mappings.ebs.encrypted") {
		t.Errorf("dynamic block not flattened; paths: %v", paths(lt))
	}
	// Nothing from the dynamic wrapper itself may leak into the paths.
	for _, a := range lt.Attrs {
		if strings.Contains(a.Path, "dynamic") || strings.Contains(a.Path, "content") {
			t.Errorf("dynamic scaffolding leaked into path %q", a.Path)
		}
		if a.Path == "for_each" {
			t.Errorf("for_each leaked into the flattened paths")
		}
	}

	provider := usage(t, cfg, "provider.aws")
	if !hasPath(provider, "endpoints.opsworks") {
		t.Errorf("nested provider block not flattened; paths: %v", paths(provider))
	}

	ami := usage(t, cfg, "data.aws_ami.amazon_linux")
	if !hasPath(ami, "filter.name") {
		t.Errorf("nested filter block not flattened; paths: %v", paths(ami))
	}
}

func TestAttributeLineNumbers(t *testing.T) {
	cfg := load(t, "../../testdata/aws5")
	flow := usage(t, cfg, "aws_flow_log.vpc")
	for _, a := range flow.Attrs {
		if a.Path == "log_group_name" {
			if a.Line != 25 {
				t.Fatalf("log_group_name line = %d, want 25", a.Line)
			}
			return
		}
	}
	t.Fatalf("log_group_name not collected; paths: %v", paths(flow))
}

func TestLocalModulesFollowedRemoteModulesRecorded(t *testing.T) {
	cfg := load(t, "../../testdata/aws5")

	if len(cfg.Dirs) != 2 {
		t.Errorf("scanned %d directories, want 2 (root + ./modules/network): %v", len(cfg.Dirs), cfg.Dirs)
	}
	want := []string{"terraform-aws-modules/vpc/aws"}
	if len(cfg.SkippedModules) != 1 || cfg.SkippedModules[0] != want[0] {
		t.Errorf("SkippedModules = %v, want %v", cfg.SkippedModules, want)
	}
}

func TestRequiredProviders(t *testing.T) {
	cfg := load(t, "../../testdata/aws5")
	if len(cfg.Requirements) != 1 {
		t.Fatalf("Requirements = %+v, want exactly one", cfg.Requirements)
	}
	req := cfg.Requirements[0]
	if req.Name != "aws" || req.Source != "hashicorp/aws" || req.Constraint != "~> 5.40" {
		t.Errorf("unexpected requirement: %+v", req)
	}
	if req.Line != 5 {
		t.Errorf("requirement line = %d, want 5", req.Line)
	}
}

func TestLoadRejectsBrokenHCL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.tf"), []byte(`resource "aws_eip" {`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected a parse error, got nil")
	}
}

func TestLoadHandlesModuleCycle(t *testing.T) {
	// a -> b -> a. Terraform itself rejects this, but the walker must terminate
	// rather than recurse until the stack runs out.
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(a, "main.tf"), "module \"b\" {\n  source = \"../b\"\n}\n")
	write(filepath.Join(b, "main.tf"), "module \"a\" {\n  source = \"../a\"\n}\n\nresource \"aws_eip\" \"x\" {\n  vpc = true\n}\n")

	cfg, err := Load(a)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Usages) != 1 {
		t.Fatalf("Usages = %d, want 1", len(cfg.Usages))
	}
}

func TestLoadRejectsFileArgument(t *testing.T) {
	if _, err := Load("../../testdata/aws5/main.tf"); err == nil {
		t.Fatal("expected an error for a file argument, got nil")
	}
}

func paths(u Usage) []string {
	out := make([]string, 0, len(u.Attrs))
	for _, a := range u.Attrs {
		out = append(out, a.Path)
	}
	return out
}
