package xconfigdotenv

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/joho/godotenv"
)

// Decoder parses .env and places values into an arbitrary Go struct.
type Decoder struct{}

// New creates a new Decoder.
func New() *Decoder { return &Decoder{} }

// Format returns the decoder format.
func (d *Decoder) Format() string {
	return "env"
}

// Unmarshal parses []byte (.env format) and fills v – a pointer to a struct.
func (d *Decoder) Unmarshal(data []byte, v any) error {
	// 1) Parse .env → map[string]string
	flatMap, err := godotenv.UnmarshalBytes(data)
	if err != nil {
		return err
	}

	// 2) Verify that v is a non-nil pointer to a struct
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("xconfigdotenv: Unmarshal: v must be a non-nil pointer to a struct, got %T", v)
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("xconfigdotenv: Unmarshal: v must point to a struct, got pointer to %s", elem.Kind())
	}

	// 3) For each key from .env, parse the string into the target field
	for rawKey, rawVal := range flatMap {
		parts := strings.Split(rawKey, "_")
		if len(parts) == 0 {
			continue
		}
		if err := assignValue(elem, parts, rawVal); err != nil {
			return fmt.Errorf("xconfigdotenv: Unmarshal: key %q: %w", rawKey, err)
		}
	}

	return nil
}

// assignValue tries to place rawVal (string) into field v (reflect.Value of a struct)
func assignValue(v reflect.Value, parts []string, rawVal string) error {
	typ := v.Type()

	// Iterate over all prefixes from longest to shortest
	for prefixLen := len(parts); prefixLen >= 1; prefixLen-- {
		prefixJoined := strings.Join(parts[:prefixLen], "_")
		normalizedPrefix := normalize(prefixJoined)

		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			// normalize the field name and its type name
			fieldNameNorm := normalize(field.Name)
			fieldTypeNameNorm := normalize(field.Type.Name())

			// if neither the field name nor its type name matches normalizedPrefix, skip it
			envTagNorm := normalize(field.Tag.Get("env"))
			if fieldNameNorm != normalizedPrefix && fieldTypeNameNorm != normalizedPrefix && envTagNorm != normalizedPrefix {
				continue
			}

			// Found a matching field - obtain it via unsafe to handle private fields
			fieldVal := getFieldValue(v, i)
			leftover := parts[prefixLen:] // segments "after" the current prefix

			// 1) If leftover is empty, this is a "leaf" field: a basic type or a pointer to a basic type
			if len(leftover) == 0 {
				return setBasicValue(fieldVal, rawVal)
			}

			// 2) Otherwise descend further or place into a container
			switch fieldVal.Kind() {
			case reflect.Pointer:
				// Pointer: if nil – create a new one; then expect a struct and recurse into it
				if fieldVal.IsNil() {
					newPtr := reflect.New(fieldVal.Type().Elem())
					if err := setWithReflect(fieldVal, newPtr); err != nil {
						return err
					}
				}
				elem := fieldVal.Elem()
				if elem.Kind() == reflect.Struct {
					return assignValue(elem, leftover, rawVal)
				}
				return fmt.Errorf("cannot descend into pointer field %q (kind %s), leftover %v", field.Name, elem.Kind(), leftover)

			case reflect.Struct:
				// Nested struct – recurse into it
				return assignValue(fieldVal, leftover, rawVal)

			case reflect.Map:
				// Map: join leftover to form the key; rawVal is the value
				if len(leftover) == 0 {
					return fmt.Errorf("map field %q but no key given (leftover is empty)", field.Name)
				}
				if fieldVal.IsNil() { // initialize if needed
					newMap := reflect.MakeMap(fieldVal.Type())
					if err := setWithReflect(fieldVal, newMap); err != nil {
						return err
					}
				}
				mapKey := strings.Join(leftover, "_")
				return setMapValue(fieldVal, mapKey, rawVal)

			case reflect.Slice:
				// Slice: leftover[0] is the index (number), leftover[1:] is nesting inside the element (if any)
				idxStr := leftover[0]
				ix, err := strconv.Atoi(idxStr)
				if err != nil {
					return fmt.Errorf("cannot parse slice index %q for field %q", idxStr, field.Name)
				}
				// If the slice is nil – initialize an empty one
				if fieldVal.IsNil() {
					newSlice := reflect.MakeSlice(fieldVal.Type(), 0, 0)
					if err := setWithReflect(fieldVal, newSlice); err != nil {
						return err
					}
				}
				// Grow the slice if needed
				curLen := fieldVal.Len()
				if ix >= curLen {
					newLen := ix + 1
					newSlice := reflect.MakeSlice(fieldVal.Type(), newLen, newLen)
					// Copy elements into the new slice
					for j := 0; j < curLen; j++ {
						elem := fieldVal.Index(j)
						target := newSlice.Index(j)
						setWithReflect(target, elem)
					}
					if err := setWithReflect(fieldVal, newSlice); err != nil {
						return err
					}
				}
				// Get the element
				elemVal := fieldVal.Index(ix)
				// If there is nesting after the index
				if len(leftover) > 1 {
					switch elemVal.Kind() {
					case reflect.Pointer:
						if elemVal.IsNil() {
							newPtr := reflect.New(elemVal.Type().Elem())
							if err := setWithReflect(elemVal, newPtr); err != nil {
								return err
							}
						}
						return assignValue(elemVal.Elem(), leftover[1:], rawVal)
					case reflect.Struct:
						return assignValue(elemVal, leftover[1:], rawVal)
					default:
						return fmt.Errorf("cannot descend into slice element kind %s for field %q", elemVal.Kind(), field.Name)
					}
				}
				// Otherwise – just assign the basic value to the element
				return setBasicValue(elemVal, rawVal)

			default:
				// Not a container, but leftover is non-empty – invalid nesting
				return fmt.Errorf("cannot descend into field %q (kind %s), leftover %v", field.Name, fieldVal.Kind(), leftover)
			}
		}
	}

	// No prefix matched – just ignore this key
	return nil
}

// getFieldValue returns the field value by index, supporting private fields via unsafe
func getFieldValue(structVal reflect.Value, fieldIndex int) reflect.Value {
	field := structVal.Field(fieldIndex)

	// If the field is exported, return it as-is
	if field.CanSet() {
		return field
	}

	// For private fields, use unsafe
	if structVal.CanAddr() {
		structType := structVal.Type()
		fieldType := structType.Field(fieldIndex)
		fieldPtr := unsafe.Pointer(uintptr(unsafe.Pointer(structVal.UnsafeAddr())) + fieldType.Offset)
		return reflect.NewAt(fieldType.Type, fieldPtr).Elem()
	}

	return field
}

// setBasicValue converts the rawVal string into the basic type fieldVal.Type()
func setBasicValue(fieldVal reflect.Value, rawVal string) error {
	// Special case: time.Duration
	if fieldVal.Type() == reflect.TypeOf(time.Duration(0)) {
		dur, err := time.ParseDuration(rawVal)
		if err != nil {
			return fmt.Errorf("cannot parse %q as Duration: %w", rawVal, err)
		}
		return setWithReflect(fieldVal, reflect.ValueOf(dur))
	}

	ft := fieldVal.Type()
	kind := ft.Kind()

	var cv reflect.Value
	switch kind {
	case reflect.String:
		cv = reflect.ValueOf(rawVal).Convert(ft)
	case reflect.Bool:
		b, err := strconv.ParseBool(rawVal)
		if err != nil {
			return fmt.Errorf("cannot parse %q as bool: %w", rawVal, err)
		}
		cv = reflect.ValueOf(b).Convert(ft)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(rawVal, 10, ft.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse %q as int: %w", rawVal, err)
		}
		cv = reflect.ValueOf(i).Convert(ft)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(rawVal, 10, ft.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse %q as uint: %w", rawVal, err)
		}
		cv = reflect.ValueOf(u).Convert(ft)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(rawVal, ft.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse %q as float: %w", rawVal, err)
		}
		cv = reflect.ValueOf(f).Convert(ft)
	case reflect.Complex64, reflect.Complex128:
		c, err := strconv.ParseComplex(rawVal, ft.Bits())
		if err != nil {
			return fmt.Errorf("cannot parse %q as complex: %w", rawVal, err)
		}
		cv = reflect.ValueOf(c).Convert(ft)
	case reflect.Pointer:
		// pointer: if nil – create it, then recursively write inside
		if fieldVal.IsNil() {
			newPtr := reflect.New(ft.Elem())
			if err := setWithReflect(fieldVal, newPtr); err != nil {
				return err
			}
		}
		return setBasicValue(fieldVal.Elem(), rawVal)
	default:
		return fmt.Errorf("unsupported kind %s for value %q", kind, rawVal)
	}

	return setWithReflect(fieldVal, cv)
}

// setWithReflect writes cv into fieldVal, supporting private fields via unsafe
func setWithReflect(fieldVal, cv reflect.Value) error {
	// Try the normal way for exported fields
	if fieldVal.CanSet() {
		fieldVal.Set(cv)
		return nil
	}

	// For private fields, use unsafe if the field is addressable
	if fieldVal.CanAddr() {
		ptr := unsafe.Pointer(fieldVal.UnsafeAddr())
		realVal := reflect.NewAt(fieldVal.Type(), ptr).Elem()
		realVal.Set(cv)
		return nil
	}

	return fmt.Errorf("cannot set field of kind %s (not addressable)", fieldVal.Kind())
}

// setMapValue puts rawVal (string) into map[string]X
func setMapValue(mapVal reflect.Value, mapKey, rawVal string) error {
	keyType := mapVal.Type().Key()
	valType := mapVal.Type().Elem()

	// Only string keys are supported
	if keyType.Kind() != reflect.String {
		return fmt.Errorf("unsupported map key type %s; only string keys allowed", keyType.Kind())
	}

	// Convert rawVal to the valType
	var cv reflect.Value
	if valType.Kind() == reflect.Interface && valType.NumMethod() == 0 {
		cv = reflect.ValueOf(rawVal)
	} else {
		tmp := reflect.New(valType).Elem()
		if err := setBasicValue(tmp, rawVal); err != nil {
			return err
		}
		cv = tmp
	}

	// Set the value in the map
	if mapVal.CanSet() {
		mapVal.SetMapIndex(reflect.ValueOf(mapKey), cv)
		return nil
	}

	// For private map fields
	if mapVal.CanAddr() {
		ptr := unsafe.Pointer(mapVal.UnsafeAddr())
		realMap := reflect.NewAt(mapVal.Type(), ptr).Elem()
		realMap.SetMapIndex(reflect.ValueOf(mapKey), cv)
		return nil
	}

	return fmt.Errorf("cannot set map key %q on unexported field", mapKey)
}

// normalize removes all '_' and lowercases the string
func normalize(s string) string {
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "_", "")
}
