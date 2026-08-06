package flat

import (
	"encoding"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sxwebdev/xconfig/internal/utils"
)

var _ Field = (*field)(nil)

type field struct {
	name      string
	meta      map[string]string
	parentTag reflect.StructTag

	tag       reflect.StructTag
	field     reflect.Value
	fieldType reflect.StructField

	// mapSync is called after field modification to sync back to map
	mapSync func()
}

// Used by standard library flag package.
func (f *field) IsBoolFlag() bool {
	return f.field.Kind() == reflect.Bool
}

func (f *field) Name() string {
	return f.name
}

// EnvName returns the name of the environment variable.
func (f *field) EnvName() string {
	words := utils.SplitNameByWords(f.name)

	// filter out empty words
	for i := 0; i < len(words); {
		if words[i] == "" {
			words = slices.Delete(words, i, i+1)
		} else {
			i++
		}
	}

	return strings.ToUpper(strings.Join(words, "_"))
}

func (f *field) Meta() map[string]string {
	return f.meta
}

func (f *field) Tag(key string) (string, bool) {
	return f.tag.Lookup(key)
}

func (f *field) ParentTag() reflect.StructTag {
	return f.parentTag
}

func (f *field) String() string {
	return f.tag.Get("default")
}

func (f *field) IsZero() bool {
	return f.field.IsValid() && f.field.IsZero()
}

var textUnmarshalerType = reflect.TypeOf(new(encoding.TextUnmarshaler)).Elem()

func (f *field) Set(value string) error {
	_, err := f.SetChanged(value)
	return err
}

// SetChanged sets value and reports whether the field's semantic value changed.
func (f *field) SetChanged(value string) (bool, error) {
	t := f.field.Type()

	if t.Implements(textUnmarshalerType) {
		changed, err := f.setUnmarshale([]byte(value))
		if err == nil && changed && f.mapSync != nil {
			f.mapSync()
		}
		return changed, err
	}

	before := reflect.New(t).Elem()
	before.Set(f.field)
	var err error
	switch f.field.Kind() {
	case reflect.String:
		err = f.setString(value)
	case reflect.Bool:
		err = f.setBool(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if t.String() == "time.Duration" {
			err = f.setDuration(value)
		} else {
			err = f.setInt(value)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		err = f.setUint(value)
	case reflect.Float32, reflect.Float64:
		err = f.setFloat(value)
	case reflect.Slice:
		err = f.setSlice(value)

		// Soon case reflect.Map:

		// Maybe case reflect.Array:

		// Why? case reflect.Complex64:
		// Why? case reflect.Complex128:

		// Never case reflect.Func:
		// Never case reflect.Chan:
		// Never case reflect.Interface:
		// Never case reflect.Pointer:
		// Never case reflect.Struct:
		// Never case reflect.UnsafePointer:
	}

	changed := err == nil && !equalIgnoringFuncs(before, f.field)
	if changed && f.mapSync != nil {
		f.mapSync()
	}

	return changed, err
}

// FieldValue is a field in a struct.
func (f *field) FieldValue() reflect.Value {
	return f.field
}

// FieldType is a field in a struct.
func (f *field) FieldType() reflect.StructField {
	return f.fieldType
}

func (f *field) setUnmarshale(value []byte) (bool, error) {
	if f.field.Kind() != reflect.Pointer {
		// A non-pointer type implements TextUnmarshaler on a value receiver, which
		// only ever sees a copy: the parsed value can never reach the field. Report
		// it instead of silently zeroing the field.
		return false, fmt.Errorf("field %s: cannot set %s from text: UnmarshalText must be declared on a pointer receiver", f.name, f.field.Type())
	}

	candidate := reflect.New(f.field.Type().Elem())
	if !f.field.IsNil() {
		// Stage on a copy of the current value, not on a zero one: UnmarshalText
		// keeps the fields it does not assign, and a failure leaves the live
		// value untouched.
		candidate.Elem().Set(f.field.Elem())
	}
	if err := callUnmarshalText(candidate, value); err != nil {
		return false, err
	}
	if f.field.IsNil() {
		f.field.Set(candidate)
		return true, nil
	}
	changed := !equalIgnoringFuncs(f.field.Elem(), candidate.Elem())
	if changed {
		f.field.Elem().Set(candidate.Elem())
	}
	return changed, nil
}

// equalIgnoringFuncs reports whether a and b hold the same value, treating every
// func as equal to every other func. Nothing can set a func from a string, so a
// func is never part of a field's value, while reflect.DeepEqual reports any two
// non-nil funcs as unequal - which would make a field carrying one look changed
// on every single Set, and republish it on every refresh tick forever. It also
// reads unexported fields, which reflect.Value.Interface rejects.
func equalIgnoringFuncs(a, b reflect.Value) bool {
	if a.Type() != b.Type() {
		return false
	}

	switch a.Kind() {
	case reflect.Func:
		return true
	case reflect.Pointer:
		if a.Pointer() == b.Pointer() {
			return true
		}
		if a.IsNil() || b.IsNil() {
			return false
		}
		return equalIgnoringFuncs(a.Elem(), b.Elem())
	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return equalIgnoringFuncs(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := range a.NumField() {
			if !equalIgnoringFuncs(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		fallthrough
	case reflect.Array:
		for i := range a.Len() {
			if !equalIgnoringFuncs(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		for _, key := range a.MapKeys() {
			bv := b.MapIndex(key)
			if !bv.IsValid() || !equalIgnoringFuncs(a.MapIndex(key), bv) {
				return false
			}
		}
		return true
	default:
		return a.Equal(b)
	}
}

func callUnmarshalText(target reflect.Value, value []byte) error {
	result := target.MethodByName("UnmarshalText").Call([]reflect.Value{reflect.ValueOf(value)})[0]
	if result.IsNil() {
		return nil
	}
	er, ok := result.Interface().(error)
	if !ok {
		return fmt.Errorf("unmarshal text: %v", result)
	}
	return er
}

func (f *field) setDuration(value string) error {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return err
	}

	f.field.SetInt(int64(duration))
	return nil
}

func (f *field) setString(value string) error {
	f.field.SetString(value)
	return nil
}

func (f *field) setBool(value string) error {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	f.field.SetBool(v)
	return nil
}

func (f *field) setInt(value string) error {
	v, err := strconv.ParseInt(value, 0, 64)
	if err != nil {
		return err
	}
	f.field.SetInt(v)
	return nil
}

func (f *field) setUint(value string) error {
	v, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return err
	}
	f.field.SetUint(v)
	return nil
}

func (f *field) setFloat(value string) error {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	f.field.SetFloat(v)
	return nil
}

func (f *field) setSlice(value string) error {
	t := f.field.Type()
	setSliceElem := setSliceElem(t.Elem())

	if setSliceElem == nil {
		return nil
	}

	values := strings.Split(value, ",")
	valuesLen := len(values)

	candidate := reflect.MakeSlice(t, valuesLen, valuesLen)

	for i, value := range values {
		err := setSliceElem(candidate.Index(i), strings.TrimSpace(value))
		if err != nil {
			return err
		}
	}

	f.field.Set(candidate)
	return nil
}

func setSliceElem(elem reflect.Type) func(reflect.Value, string) error {
	switch elem.Kind() {
	case reflect.String:
		return setSliceElemString

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if elem.String() == "time.Duration" {
			return setSliceElemDuration
		}

		return setSliceElemInt

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return setSliceElemUint

	case reflect.Float32, reflect.Float64:
		return setSliceElemFloat
	}

	return nil
}

func setSliceElemString(f reflect.Value, value string) error {
	f.SetString(value)
	return nil
}

func setSliceElemDuration(f reflect.Value, value string) error {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return err
	}

	f.SetInt(int64(duration))
	return nil
}

func setSliceElemInt(f reflect.Value, value string) error {
	v, err := strconv.ParseInt(value, 0, 64)
	if err != nil {
		return err
	}
	f.SetInt(v)
	return nil
}

func setSliceElemUint(f reflect.Value, value string) error {
	v, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return err
	}
	f.SetUint(v)
	return nil
}

func setSliceElemFloat(f reflect.Value, value string) error {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	f.SetFloat(v)
	return nil
}
