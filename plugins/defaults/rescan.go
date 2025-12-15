package defaults

import (
	"reflect"
	"strings"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

type presentFieldsProvider interface {
	PresentFields() map[string]struct{}
}

// NewWithRescan returns a defaults plugin that rescans the structure
// before applying defaults. This is useful when you want to apply defaults
// after loading configuration files that may have created new structs in maps.
func NewWithRescan(present presentFieldsProvider) plugins.Plugin {
	return &rescanVisitor{present: present}
}

type rescanVisitor struct {
	conf    any
	present presentFieldsProvider
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

	present := map[string]struct{}{}
	if v.present != nil {
		present = v.present.PresentFields()
	}

	// Register metadata and apply defaults only to zero fields
	for _, f := range fields {
		value, ok := f.Tag(tag)
		if !ok {
			continue
		}

		// Register metadata for usage/documentation
		f.Meta()[tag] = value

		// If the field was explicitly present in a loaded config file, do not override it.
		if len(present) > 0 {
			if p, ok := fieldConfigPath(v.conf, f.Name()); ok {
				if _, exists := present[p]; exists {
					continue
				}
			}
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

func fieldConfigPath(conf any, flatName string) (string, bool) {
	t := reflect.TypeOf(conf)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return "", false
	}

	segments := strings.Split(flatName, ".")
	pathParts := make([]string, 0, len(segments))

	cur := t
	for _, seg := range segments {
		for cur.Kind() == reflect.Ptr {
			cur = cur.Elem()
		}

		switch cur.Kind() {
		case reflect.Struct:
			sf, ok := findFieldByFlatSegment(cur, seg)
			if !ok {
				return "", false
			}
			name, ok := fileFieldName(sf)
			if !ok {
				return "", false
			}
			pathParts = append(pathParts, name)
			cur = sf.Type
		case reflect.Map:
			// Map keys are dynamic and appear in flat field names as-is.
			pathParts = append(pathParts, seg)
			cur = cur.Elem()
		default:
			return "", false
		}
	}

	return strings.Join(pathParts, "."), true
}

func findFieldByFlatSegment(structType reflect.Type, seg string) (reflect.StructField, bool) {
	// Fast path: exact Go field name.
	if sf, ok := structType.FieldByName(seg); ok {
		return sf, true
	}

	// Otherwise, try to match by tags. This matters when flat names were overridden
	// with `xconfig:"..."` (often snake_case), because FieldByName() won't find it.
	for i := 0; i < structType.NumField(); i++ {
		sf := structType.Field(i)
		if !sf.IsExported() {
			continue
		}

		if xname, ok := sf.Tag.Lookup("xconfig"); ok && xname == seg {
			return sf, true
		}

		if yamlTag := sf.Tag.Get("yaml"); yamlTag != "" {
			parts := strings.Split(yamlTag, ",")
			if parts[0] == seg {
				return sf, true
			}
		}

		if jsonTag := sf.Tag.Get("json"); jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] == seg {
				return sf, true
			}
		}
	}

	return reflect.StructField{}, false
}

func fileFieldName(field reflect.StructField) (string, bool) {
	if yamlTag := field.Tag.Get("yaml"); yamlTag != "" {
		parts := strings.Split(yamlTag, ",")
		if parts[0] == "-" {
			return "", false
		}
		if parts[0] != "" {
			return parts[0], true
		}
	}

	if jsonTag := field.Tag.Get("json"); jsonTag != "" {
		parts := strings.Split(jsonTag, ",")
		if parts[0] == "-" {
			return "", false
		}
		if parts[0] != "" {
			return parts[0], true
		}
	}

	if name, ok := field.Tag.Lookup("xconfig"); ok && name != "" {
		return name, true
	}

	// No tags: config keys are typically lower-cased (e.g. "parser" for Go field "Parser").
	return strings.ToLower(field.Name), true
}
