// Package flat provides a flat view of an arbitrary nested structs.
package flat

import (
	"errors"
	"reflect"
)

// ErrUnexpectedType is returned when flatten sees an unsupported type.
var ErrUnexpectedType = errors.New("unexpected type, expecting a pointer to struct")

// Fields is a slice of Field.
type Fields []Field

// Field describe an interface to our flat structs fields.
type Field interface {
	Name() string
	EnvName() string
	Tag(key string) (string, bool)
	ParentTag() reflect.StructTag

	Meta() map[string]string

	String() string
	Set(value string) error
	IsZero() bool

	FieldValue() reflect.Value
	FieldType() reflect.StructField
}

// View provides a flat view of the provided structs an array of fields.
// sub-struct fields are prefixed with the struct key (not type) followed by a dot,
// this is repeated for each nested level.
func View(s any) (Fields, error) {
	rs, err := unwrap(s)
	if err != nil {
		return nil, err
	}

	return walkStruct("", rs)
}

func walkStruct(prefix string, rs reflect.Value) ([]Field, error) {
	return walkStructWithParentTags(prefix, rs, "")
}

func walkStructWithParentTags(prefix string, rs reflect.Value, parentTags reflect.StructTag) ([]Field, error) {
	fields := []Field{}

	ts := rs.Type()
	for i := range rs.NumField() {
		fv := rs.Field(i)
		ft := ts.Field(i)

		// skip if field is not exported
		if !ft.IsExported() {
			continue
		}

		switch fv.Kind() {
		case reflect.Struct:
			structPrefix := prefix
			if !ft.Anonymous {
				// Unless it is anonymous struct, append the field name to the prefix.
				if structPrefix == "" {
					structPrefix = ft.Name
				} else {
					structPrefix = structPrefix + "." + ft.Name
				}
			}
			// Pass the struct's tags to children
			fs, err := walkStructWithParentTags(structPrefix, fv, ft.Tag)
			if err != nil {
				return nil, err
			}
			fields = append(fields, fs...)
		case reflect.Map:
			// Handle maps with struct values
			if fv.IsNil() {
				continue
			}

			mapElemType := fv.Type().Elem()
			if mapElemType.Kind() == reflect.Struct {
				mapPrefix := prefix
				if mapPrefix == "" {
					mapPrefix = ft.Name
				} else {
					mapPrefix = mapPrefix + "." + ft.Name
				}

				// Collect all keys first to avoid issues with modifying map during iteration
				keys := make([]reflect.Value, 0)
				iter := fv.MapRange()
				for iter.Next() {
					keys = append(keys, iter.Key())
				}

				// Process each key
				for _, key := range keys {
					val := fv.MapIndex(key)

					// Create a prefix with the map key
					keyPrefix := mapPrefix + "." + key.String()

					// Create an addressable copy of the map value
					addressableVal := reflect.New(mapElemType).Elem()
					addressableVal.Set(val)

					// Walk the struct value - this will create fields pointing to addressableVal
					fs, err := walkStructWithParentTags(keyPrefix, addressableVal, ft.Tag)
					if err != nil {
						return nil, err
					}

					// Set mapSync callback for all fields to sync back to the map
					mapValue := fv            // capture map
					mapKey := key             // capture key
					syncVal := addressableVal // capture addressable value
					for _, fld := range fs {
						if f, ok := fld.(*field); ok {
							prev := f.mapSync
							f.mapSync = func() {
								if prev != nil {
									prev()
								}
								mapValue.SetMapIndex(mapKey, syncVal)
							}
						}
					}

					fields = append(fields, fs...)
				}
			}
		default:
			fieldName := ft.Name

			// unless it is override
			if name, ok := ft.Tag.Lookup("xconfig"); ok && name != "" {
				fieldName = name
			}

			if prefix != "" {
				fieldName = prefix + "." + fieldName
			}

			fields = append(fields, &field{
				name:      fieldName,
				meta:      make(map[string]string, 5),
				tag:       ft.Tag,
				parentTag: parentTags,
				field:     fv,
				fieldType: ft,
			})
		}
	}

	return fields, nil
}

func unwrap(s any) (reflect.Value, error) {
	rs := reflect.ValueOf(s)

	if k := rs.Kind(); k != reflect.Ptr {
		return rs, ErrUnexpectedType
	}

	rs = reflect.Indirect(rs)

	if rs.Kind() == reflect.Interface {
		rs = rs.Elem()
	}

	rs = reflect.Indirect(rs)

	if rs.Kind() != reflect.Struct {
		return rs, ErrUnexpectedType
	}

	return rs, nil
}
