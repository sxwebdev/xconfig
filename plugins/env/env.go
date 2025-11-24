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
	fields flat.Fields
	prefix string
}

func makeEnvName(prefix, name string) string {
	if prefix != "" {
		name = strings.ToUpper(prefix) + "_" + name
	}

	return name
}

func (v *visitor) Visit(f flat.Fields) error {
	v.fields = f

	for _, f := range v.fields {
		name, ok := f.Tag(tag)
		if !ok || name == "" {
			name = v.buildEnvName(f)
		} else {
			// If explicit tag is provided, still apply prefix
			name = makeEnvName(v.prefix, name)
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
		return makeEnvName(v.prefix, f.EnvName())
	}

	// Check if parent struct has an env tag
	parentTag := f.ParentTag()
	if parentTag != "" {
		if parentEnvTag, ok := parentTag.Lookup(tag); ok && parentEnvTag != "" {
			// Build the env name using the parent's env tag as prefix
			// Take the last part of the field name (the actual field, not the struct)
			lastPart := parts[len(parts)-1]
			envName := parentEnvTag + "_" + strings.ToUpper(toSnakeCase(lastPart))
			return makeEnvName(v.prefix, envName)
		}
	}

	// No parent tag found, use default behavior
	return makeEnvName(v.prefix, f.EnvName())
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
	for _, f := range v.fields {
		name, ok := f.Meta()[tag]
		if !ok || name == "-" {
			continue
		}

		value, ok := os.LookupEnv(name)

		if !ok {
			continue
		}

		err := f.Set(value)
		if err != nil {
			return err
		}
	}

	return nil
}
