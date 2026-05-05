// Package env provides environment variables support for xconfig
package env

import (
	"os"
	"strings"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

const tag = "env"

func init() {
	plugins.RegisterTag(tag)
}

// New returns an EnvSet.
func New(prefix string) plugins.Plugin {
	return &visitor{
		prefix: prefix,
	}
}

type visitor struct {
	conf   any
	fields flat.Fields
	prefix string
}

// Walk captures the conf reference so Parse can re-flatten and expand
// slice/map fields based on env variables.
func (v *visitor) Walk(conf any) error {
	v.conf = conf
	return nil
}

func (v *visitor) Visit(f flat.Fields) error {
	v.fields = f

	// Use the same context-aware naming logic as Parse so that env-tagged
	// fields inside slice/map elements get e.g. MYAPP_NODES_0_USE_TLS
	// instead of MYAPP_USE_TLS (the latter would drop the index and
	// collide across elements). Walking with no keys means no containers
	// are expanded — we only resolve names for entries already present.
	var nameMap map[string]string
	if v.conf != nil {
		m, err := flat.ExpandContainersFromKeys(v.conf, v.prefix, nil)
		if err != nil {
			return err
		}
		nameMap = m
	}

	for _, f := range v.fields {
		if name, ok := nameMap[f.Name()]; ok {
			f.Meta()[tag] = name
			continue
		}
		name, ok := f.Tag(tag)
		if !ok || name == "" {
			name = v.buildEnvName(f)
		} else {
			// If explicit tag is provided, still apply prefix
			name = flat.MakeEnvName(v.prefix, name)
		}
		f.Meta()[tag] = name
	}

	return nil
}

// buildEnvName constructs environment variable name considering parent struct tags
func (v *visitor) buildEnvName(f flat.Field) string {
	fieldName := f.Name()
	parts := strings.Split(fieldName, ".")

	if len(parts) == 1 {
		// Simple field without nesting
		return flat.MakeEnvName(v.prefix, f.EnvName())
	}

	// Check if parent struct has an env tag
	parentTag := f.ParentTag()
	if parentTag != "" {
		if parentEnvTag, ok := parentTag.Lookup(tag); ok && parentEnvTag != "" {
			// Build the env name using the parent's env tag as prefix.
			// Keep all path segments after the field whose tag we're using —
			// for slice/map paths the segments between the parent and the
			// leaf carry the index/key and must not be dropped.
			tail := parts[len(parts)-1]
			middle := parts[1 : len(parts)-1]
			segments := []string{parentEnvTag}
			for _, m := range middle {
				segments = append(segments, strings.ToUpper(toSnakeCase(m)))
			}
			segments = append(segments, strings.ToUpper(toSnakeCase(tail)))
			return flat.MakeEnvName(v.prefix, strings.Join(segments, "_"))
		}
	}

	// No parent tag found, use default behavior
	return flat.MakeEnvName(v.prefix, f.EnvName())
}

// toSnakeCase converts PascalCase/camelCase to snake_case
func toSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result = append(result, '_')
		}
		result = append(result, r)
	}
	return string(result)
}

func (v *visitor) Parse() error {
	// Expand slices/maps based on env-vars BEFORE re-flattening so newly
	// created entries are visible to subsequent flat.View calls.
	envKeys := envKeys()
	nameMap, err := flat.ExpandContainersFromKeys(v.conf, v.prefix, envKeys)
	if err != nil {
		return err
	}

	fields, err := flat.View(v.conf)
	if err != nil {
		return err
	}

	// Re-stamp env names on fields (including newly created ones). For fields
	// known to nameMap (slice/map entries) use the precomputed name; for the
	// rest fall back to the existing logic so explicit env tags still win.
	for _, f := range fields {
		name, ok := nameMap[f.Name()]
		if !ok {
			tagName, hasTag := f.Tag(tag)
			if hasTag && tagName != "" {
				name = flat.MakeEnvName(v.prefix, tagName)
			} else {
				name = v.buildEnvName(f)
			}
		}
		f.Meta()[tag] = name
	}
	v.fields = fields

	for _, f := range v.fields {
		name, ok := f.Meta()[tag]
		if !ok || name == "-" {
			continue
		}

		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}

		if err := f.Set(value); err != nil {
			return err
		}
	}

	return nil
}

// envKeys returns the names of currently-set environment variables.
func envKeys() []string {
	envs := os.Environ()
	keys := make([]string, 0, len(envs))
	for _, e := range envs {
		if eq := strings.IndexByte(e, '='); eq >= 0 {
			keys = append(keys, e[:eq])
		}
	}
	return keys
}
