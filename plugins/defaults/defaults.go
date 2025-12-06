// Package defaults provides default values for xconfig
package defaults

import (
	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

const tag = "default"

func init() {
	plugins.RegisterTag(tag)
}

// New returns a defaults plugin.
func New() plugins.Plugin {
	return &visitor{applyDefaults: true}
}

// NewMetaOnly returns a defaults plugin that only registers metadata
// without applying default values. This is useful when you want to
// register defaults for usage/documentation but apply them later.
func NewMetaOnly() plugins.Plugin {
	return &visitor{applyDefaults: false}
}

type visitor struct {
	fields        flat.Fields
	applyDefaults bool
}

func (v *visitor) Visit(f flat.Fields) error {
	v.fields = f

	for _, f := range v.fields {
		value, ok := f.Tag(tag)
		if !ok {
			continue
		}

		f.Meta()[tag] = value
	}
	return nil
}

func (v *visitor) Parse() error {
	// If applyDefaults is false, skip applying values (only metadata was registered)
	if !v.applyDefaults {
		return nil
	}

	for _, f := range v.fields {
		value, ok := f.Meta()[tag]
		if !ok {
			continue
		}

		// Only set default if field is zero (empty)
		if !f.IsZero() {
			continue
		}

		err := f.Set(value)
		if err != nil {
			return err
		}
	}

	return nil
}
