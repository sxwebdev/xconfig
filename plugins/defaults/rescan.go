package defaults

import (
	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

// NewWithRescan returns a defaults plugin that rescans the structure
// before applying defaults. This is useful when you want to apply defaults
// after loading configuration files that may have created new structs in maps.
func NewWithRescan() plugins.Plugin {
	return &rescanVisitor{}
}

type rescanVisitor struct {
	conf any
}

func (v *rescanVisitor) Walk(conf any) error {
	v.conf = conf
	return nil
}

func (v *rescanVisitor) Parse() error {
	// Rescan the structure to get all fields including those in maps
	fields, err := flat.View(v.conf)
	if err != nil {
		return err
	}

	// Register metadata and apply defaults only to zero fields
	for _, f := range fields {
		value, ok := f.Tag(tag)
		if !ok {
			continue
		}

		// Register metadata for usage/documentation
		f.Meta()[tag] = value

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
