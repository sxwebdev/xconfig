// Package flat provides a flat view of an arbitrary nested structs.
package flat

import (
	"errors"
	"reflect"
	"strconv"
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
			if fv.IsNil() {
				continue
			}

			mapElemType := fv.Type().Elem()
			elemKind := mapElemType.Kind()
			isPtrToStruct := elemKind == reflect.Pointer && mapElemType.Elem().Kind() == reflect.Struct
			isStruct := elemKind == reflect.Struct

			mapPrefix := prefix
			if mapPrefix == "" {
				mapPrefix = ft.Name
			} else {
				mapPrefix = mapPrefix + "." + ft.Name
			}

			// Collect all keys first to avoid issues with modifying map during iteration.
			keys := make([]reflect.Value, 0)
			iter := fv.MapRange()
			for iter.Next() {
				keys = append(keys, iter.Key())
			}

			for _, key := range keys {
				val := fv.MapIndex(key)
				keyPrefix := mapPrefix + "." + key.String()

				switch {
				case isStruct:
					// Map with struct values: walk into the value as a struct.
					addressableVal := reflect.New(mapElemType).Elem()
					addressableVal.Set(val)

					fs, err := walkStructWithParentTags(keyPrefix, addressableVal, ft.Tag)
					if err != nil {
						return nil, err
					}

					mapValue := fv
					mapKey := key
					syncVal := addressableVal
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

				case isPtrToStruct:
					// Map with *struct values: dereference and walk.
					if val.IsNil() {
						continue
					}
					addressableVal := reflect.New(mapElemType.Elem())
					addressableVal.Elem().Set(val.Elem())

					fs, err := walkStructWithParentTags(keyPrefix, addressableVal.Elem(), ft.Tag)
					if err != nil {
						return nil, err
					}

					mapValue := fv
					mapKey := key
					syncVal := addressableVal
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

				default:
					// Map with primitive (scalar) values: emit one Field per entry.
					addressableVal := reflect.New(mapElemType).Elem()
					addressableVal.Set(val)

					f := newMapEntryField(keyPrefix, ft, addressableVal, parentTags)
					mapValue := fv
					mapKey := key
					syncVal := addressableVal
					f.mapSync = func() {
						mapValue.SetMapIndex(mapKey, syncVal)
					}
					fields = append(fields, f)
				}
			}
		case reflect.Slice:
			// Handle slices whose elements are structs (or pointers to structs).
			// Primitive-element slices are still emitted as a single Field so
			// `default:"a,b,c"` on the slice itself continues to work via
			// field.Set (see field.setSlice).
			sliceElemType := fv.Type().Elem()
			elemType := sliceElemType
			if elemType.Kind() == reflect.Pointer {
				elemType = elemType.Elem()
			}
			if elemType.Kind() != reflect.Struct {
				fields = append(fields, newScalarField(prefix, ft, fv, parentTags))
				continue
			}

			if fv.IsNil() || fv.Len() == 0 {
				continue
			}

			slicePrefix := prefix
			if slicePrefix == "" {
				slicePrefix = ft.Name
			} else {
				slicePrefix = slicePrefix + "." + ft.Name
			}

			for i := 0; i < fv.Len(); i++ {
				elemVal := fv.Index(i)
				if elemVal.Kind() == reflect.Pointer {
					if elemVal.IsNil() {
						continue
					}
					elemVal = elemVal.Elem()
				}

				// Slice elements are directly addressable; mutations via the
				// returned Field values persist in place, so no sync callback is
				// needed (unlike map values).
				indexPrefix := slicePrefix + "." + strconv.Itoa(i)
				fs, err := walkStructWithParentTags(indexPrefix, elemVal, ft.Tag)
				if err != nil {
					return nil, err
				}
				fields = append(fields, fs...)
			}
		default:
			fields = append(fields, newScalarField(prefix, ft, fv, parentTags))
		}
	}

	return fields, nil
}

// newMapEntryField builds a Field representing a single entry of a map with
// scalar (non-struct) value type. The full path (e.g. "Tags.foo") is used as
// the field name so EnvName() formats it as "TAGS_FOO" via SplitNameByWords.
// Tags from the parent map field (e.g. `env:"TAGS"`) are exposed through
// ParentTag() so the env plugin can build a custom prefix.
func newMapEntryField(name string, ft reflect.StructField, fv reflect.Value, _ reflect.StructTag) *field {
	return &field{
		name:      name,
		meta:      make(map[string]string, 5),
		tag:       reflect.StructTag(""),
		parentTag: ft.Tag,
		field:     fv,
		fieldType: ft,
	}
}

func newScalarField(prefix string, ft reflect.StructField, fv reflect.Value, parentTags reflect.StructTag) Field {
	fieldName := ft.Name

	// unless it is override
	if name, ok := ft.Tag.Lookup("xconfig"); ok && name != "" {
		fieldName = name
	}

	if prefix != "" {
		fieldName = prefix + "." + fieldName
	}

	return &field{
		name:      fieldName,
		meta:      make(map[string]string, 5),
		tag:       ft.Tag,
		parentTag: parentTags,
		field:     fv,
		fieldType: ft,
	}
}

func unwrap(s any) (reflect.Value, error) {
	rs := reflect.ValueOf(s)

	if k := rs.Kind(); k != reflect.Pointer {
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
