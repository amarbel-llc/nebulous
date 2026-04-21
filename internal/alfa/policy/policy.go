// Package policy parses and validates the nebulous.toml policy file
// plus the URL templates inside it.
//
// The policy file is a single TOML document containing an array of
// [[policy]] entries. Every policy has an id, a URL template, a
// default browser isolation mode, and an ordered list of captures.
// See RFC 0001 § Policy Format for the semantics and the archive
// orchestrator design doc at docs/plans/2026-04-19-orchestrator-design.md
// for the integration context.
package policy

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"

	toml "github.com/pelletier/go-toml/v2"
)

// Policy is one [[policy]] entry.
type Policy struct {
	ID        string    `toml:"id"`
	URL       string    `toml:"url"`
	Isolation string    `toml:"isolation"`
	Captures  []Capture `toml:"capture"`
}

// Capture is one [[policy.capture]] entry.
type Capture struct {
	Name       string         `toml:"name"`
	Format     string         `toml:"format"`
	Browser    string         `toml:"browser"`
	Options    map[string]any `toml:"options"`
	Split      bool           `toml:"split"`
	Extensions []Extension    `toml:"extensions"`
	Flags      []string       `toml:"flags"`
}

// Extension identifies a browser extension by id + version.
type Extension struct {
	ID      string `toml:"id"`
	Version string `toml:"version"`
}

// TemplateContext is the root passed to text/template during URL
// expansion. Policies reference story fields as `{{.Story.Permalink}}`.
type TemplateContext struct {
	Story Story
}

// Story carries the fields a URL template may reference. Extensible —
// adding a field is backwards-compatible as long as no existing
// template references a newly-shadowed name.
type Story struct {
	Hash      string
	Permalink string
	Title     string
}

// fileShape matches the on-disk TOML: an array of policies.
type fileShape struct {
	Policies []Policy `toml:"policy"`
}

// LoadAll reads and validates the policy file at path.
func LoadAll(path string) ([]Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	var shape fileShape
	if err := toml.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("policy: parse %s: %w", path, err)
	}

	for i := range shape.Policies {
		applyDefaults(&shape.Policies[i])
	}
	if err := validate(shape.Policies); err != nil {
		return nil, fmt.Errorf("policy: validate %s: %w", path, err)
	}
	return shape.Policies, nil
}

func applyDefaults(p *Policy) {
	if p.Isolation == "" {
		p.Isolation = "fresh"
	}
	for i := range p.Captures {
		if p.Captures[i].Browser == "" {
			p.Captures[i].Browser = "firefox"
		}
	}
}

var (
	allowedFormats = map[string]bool{
		"text":              true,
		"pdf":               true,
		"screenshot":        true,
		"mhtml":             true,
		"a11y":              true,
		"html-monolith":     true,
		"markdown-full":     true,
		"markdown-reader":   true,
		"markdown-selector": true,
	}
	allowedBrowsers   = map[string]bool{"firefox": true, "chrome": true}
	allowedIsolations = map[string]bool{"fresh": true, "session": true, "shared": true}
)

func allowedFormatsDisplay() string {
	keys := make([]string, 0, len(allowedFormats))
	for k := range allowedFormats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "{" + strings.Join(keys, ", ") + "}"
}

func validate(pols []Policy) error {
	seen := make(map[string]bool, len(pols))
	for i, p := range pols {
		if p.ID == "" {
			return fmt.Errorf("policies[%d].id is required", i)
		}
		if seen[p.ID] {
			return fmt.Errorf("policies[%d].id %q is duplicated", i, p.ID)
		}
		seen[p.ID] = true

		if p.URL == "" {
			return fmt.Errorf("policies[%d].url is required", i)
		}
		if !allowedIsolations[p.Isolation] {
			return fmt.Errorf("policies[%d].isolation %q not in {fresh, session, shared}", i, p.Isolation)
		}
		if len(p.Captures) == 0 {
			return fmt.Errorf("policies[%d] requires at least one capture", i)
		}
		captureNames := make(map[string]bool, len(p.Captures))
		for j, c := range p.Captures {
			if c.Name == "" {
				return fmt.Errorf("policies[%d].capture[%d].name is required", i, j)
			}
			if captureNames[c.Name] {
				return fmt.Errorf("policies[%d].capture[%d].name %q is duplicated", i, j, c.Name)
			}
			captureNames[c.Name] = true
			if !allowedFormats[c.Format] {
				return fmt.Errorf("policies[%d].capture[%d].format %q not in %s", i, j, c.Format, allowedFormatsDisplay())
			}
			if !allowedBrowsers[c.Browser] {
				return fmt.Errorf("policies[%d].capture[%d].browser %q not in {firefox, chrome}", i, j, c.Browser)
			}
		}
	}
	return nil
}

// ExpandURL renders tmpl via Go text/template with ctx as dot.
// Strict mode: unknown field references produce an error rather than
// silently substituting empty strings.
func ExpandURL(tmpl string, ctx TemplateContext) (string, error) {
	t, err := template.New("url").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("policy: parse url template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("policy: expand url template: %w", err)
	}
	return buf.String(), nil
}
