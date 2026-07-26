// Package config walks a Terraform configuration and records what it actually
// uses: every resource, data and provider block, and the flattened set of
// attribute paths set inside them.
//
// The walker is deliberately syntactic. It never evaluates expressions, never
// contacts a registry and never needs credentials or state, so a scan is
// reproducible from the checkout alone.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Kind is the sort of top-level block a Usage came from.
type Kind string

const (
	KindResource Kind = "resource"
	KindData     Kind = "data"
	KindProvider Kind = "provider"
)

// Attr is one attribute or nested block header found inside a block, recorded
// as a dotted path relative to the block that contains it. A `dynamic` block is
// flattened to the block type it generates, so
//
//	dynamic "ebs_block_device" { content { encrypted = true } }
//
// records `ebs_block_device.encrypted` exactly like the static form would.
type Attr struct {
	Path  string `json:"path"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Block bool   `json:"block,omitempty"` // true when the path names a nested block, not an argument
}

// Usage is one resource, data or provider block in the configuration.
type Usage struct {
	Kind    Kind   `json:"kind"`
	Type    string `json:"type"` // "aws_instance"; the provider name for KindProvider
	Name    string `json:"name"` // local name; empty for provider blocks
	Address string `json:"address"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Attrs   []Attr `json:"attrs"`
}

// ProviderRequirement is one entry of a `required_providers` block.
type ProviderRequirement struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Constraint string `json:"constraint"`
	File       string `json:"file"`
	Line       int    `json:"line"`
}

// Config is everything the walker found across the root directory and every
// local module reachable from it.
type Config struct {
	Root         string
	Usages       []Usage
	Requirements []ProviderRequirement
	// Dirs is every directory that was parsed, root first, in visit order.
	Dirs []string
	// SkippedModules lists `module` sources that were not followed (registry,
	// git, S3, ...). v1 only resolves local paths, and silently missing half a
	// configuration would be worse than saying so.
	SkippedModules []string
}

// Load parses dir and every local module reachable from it. Directories are
// visited once; a module cycle terminates instead of recursing forever.
func Load(dir string) (*Config, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}

	cfg := &Config{Root: abs}
	seen := map[string]bool{}
	queue := []string{abs}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true

		local, err := loadDir(cur)
		if err != nil {
			return nil, err
		}
		cfg.Dirs = append(cfg.Dirs, cur)
		cfg.Usages = append(cfg.Usages, local.usages...)
		cfg.Requirements = append(cfg.Requirements, local.requirements...)
		cfg.SkippedModules = append(cfg.SkippedModules, local.skipped...)
		for _, rel := range local.localModules {
			queue = append(queue, filepath.Clean(filepath.Join(cur, rel)))
		}
	}
	sort.Strings(cfg.SkippedModules)
	cfg.SkippedModules = dedupe(cfg.SkippedModules)
	return cfg, nil
}

type dirResult struct {
	usages       []Usage
	requirements []ProviderRequirement
	localModules []string
	skipped      []string
}

// loadDir parses the *.tf files directly inside dir. Subdirectories are only
// reached through `module` blocks, matching Terraform's own scoping rules.
func loadDir(dir string) (dirResult, error) {
	var res dirResult

	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)

	parser := hclparse.NewParser()
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return res, err
		}
		file, diags := parser.ParseHCL(src, f)
		if diags.HasErrors() {
			return res, fmt.Errorf("parse %s: %s", f, diags.Error())
		}
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			return res, fmt.Errorf("parse %s: unexpected body type", f)
		}
		collect(body, &res)
	}
	return res, nil
}

// collect walks the top-level blocks of one file.
func collect(body *hclsyntax.Body, res *dirResult) {
	for _, b := range body.Blocks {
		switch b.Type {
		case "resource", "data":
			if len(b.Labels) < 2 {
				continue
			}
			kind := KindResource
			addr := b.Labels[0] + "." + b.Labels[1]
			if b.Type == "data" {
				kind = KindData
				addr = "data." + addr
			}
			res.usages = append(res.usages, Usage{
				Kind:    kind,
				Type:    b.Labels[0],
				Name:    b.Labels[1],
				Address: addr,
				File:    b.TypeRange.Filename,
				Line:    b.TypeRange.Start.Line,
				Attrs:   flatten(b.Body, ""),
			})
		case "provider":
			if len(b.Labels) < 1 {
				continue
			}
			res.usages = append(res.usages, Usage{
				Kind:    KindProvider,
				Type:    b.Labels[0],
				Address: "provider." + b.Labels[0],
				File:    b.TypeRange.Filename,
				Line:    b.TypeRange.Start.Line,
				Attrs:   flatten(b.Body, ""),
			})
		case "module":
			source, ok := stringAttr(b.Body, "source")
			if !ok {
				continue
			}
			if isLocalSource(source) {
				res.localModules = append(res.localModules, source)
			} else {
				res.skipped = append(res.skipped, source)
			}
		case "terraform":
			for _, inner := range b.Body.Blocks {
				if inner.Type == "required_providers" {
					res.requirements = append(res.requirements, requiredProviders(inner.Body)...)
				}
			}
		}
	}
}

// flatten records every attribute and nested block inside body as a dotted path
// prefixed by prefix.
func flatten(body *hclsyntax.Body, prefix string) []Attr {
	var out []Attr
	for name, attr := range body.Attributes {
		out = append(out, Attr{
			Path: join(prefix, name),
			File: attr.SrcRange.Filename,
			Line: attr.SrcRange.Start.Line,
		})
	}
	for _, b := range body.Blocks {
		name := b.Type
		inner := b.Body

		// `dynamic "x" { content { ... } }` generates blocks of type x, so it is
		// recorded under x. Anything outside `content` (for_each, iterator,
		// labels) describes the repetition, not the generated schema.
		if b.Type == "dynamic" {
			if len(b.Labels) == 0 {
				continue
			}
			name = b.Labels[0]
			inner = nil
			for _, c := range b.Body.Blocks {
				if c.Type == "content" {
					inner = c.Body
					break
				}
			}
			if inner == nil {
				// A dynamic block with no content block still proves the
				// generated block type is in use.
				out = append(out, Attr{
					Path:  join(prefix, name),
					File:  b.TypeRange.Filename,
					Line:  b.TypeRange.Start.Line,
					Block: true,
				})
				continue
			}
		}

		out = append(out, Attr{
			Path:  join(prefix, name),
			File:  b.TypeRange.Filename,
			Line:  b.TypeRange.Start.Line,
			Block: true,
		})
		out = append(out, flatten(inner, join(prefix, name))...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// requiredProviders reads `name = { source = ..., version = ... }` entries.
func requiredProviders(body *hclsyntax.Body) []ProviderRequirement {
	var out []ProviderRequirement
	for name, attr := range body.Attributes {
		req := ProviderRequirement{
			Name: name,
			File: attr.SrcRange.Filename,
			Line: attr.SrcRange.Start.Line,
		}
		if obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr); ok {
			for _, item := range obj.Items {
				key, ok := objectKey(item.KeyExpr)
				if !ok {
					continue
				}
				val, ok := literalString(item.ValueExpr)
				if !ok {
					continue
				}
				switch key {
				case "source":
					req.Source = val
				case "version":
					req.Constraint = val
				}
			}
		} else if val, ok := literalString(attr.Expr); ok {
			// Legacy shorthand: aws = "~> 5.0"
			req.Constraint = val
		}
		out = append(out, req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func objectKey(expr hclsyntax.Expression) (string, bool) {
	if wrapped, ok := expr.(*hclsyntax.ObjectConsKeyExpr); ok {
		if trav, diags := hcl.AbsTraversalForExpr(wrapped.Wrapped); !diags.HasErrors() && len(trav) == 1 {
			return trav.RootName(), true
		}
		return literalString(wrapped.Wrapped)
	}
	return literalString(expr)
}

// literalString returns the value of a constant string expression. Anything
// interpolated is reported as not-a-literal rather than guessed at.
func literalString(expr hclsyntax.Expression) (string, bool) {
	val, diags := expr.Value(nil)
	if diags.HasErrors() || val.IsNull() || !val.IsKnown() || val.Type().FriendlyName() != "string" {
		return "", false
	}
	return val.AsString(), true
}

func stringAttr(body *hclsyntax.Body, name string) (string, bool) {
	attr, ok := body.Attributes[name]
	if !ok {
		return "", false
	}
	return literalString(attr.Expr)
}

// isLocalSource reports whether a module source is a relative filesystem path,
// which is the only kind v1 follows.
func isLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
