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
	t := f.field.Type()

	if t.Implements(textUnmarshalerType) {
		err := f.setUnmarshale([]byte(value))
		if err == nil && f.mapSync != nil {
			f.mapSync()
		}
		return err
	}

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
		// Never case reflect.Ptr:
		// Never case reflect.Struct:
		// Never case reflect.UnsafePointer:
	}

	if err == nil && f.mapSync != nil {
		f.mapSync()
	}

	return err
}

// FieldValue is a field in a struct.
func (f *field) FieldValue() reflect.Value {
	return f.field
}

// FieldType is a field in a struct.
func (f *field) FieldType() reflect.StructField {
	return f.fieldType
}

func (f *field) setUnmarshale(value []byte) error {
	if f.field.IsNil() {
		f.field.Set(reflect.New(f.field.Type().Elem()))
	}

	ut := f.field.MethodByName("UnmarshalText")

	err := ut.Call([]reflect.Value{reflect.ValueOf(value)})[0]

	if err.IsNil() {
		return nil
	}

	er, ok := err.Interface().(error)
	if !ok {
		return fmt.Errorf("unmarshal text: %v", err)
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
	f.field.SetBool(v)
	return err
}

func (f *field) setInt(value string) error {
	v, err := strconv.ParseInt(value, 0, 64)
	f.field.SetInt(v)
	return err
}

func (f *field) setUint(value string) error {
	v, err := strconv.ParseUint(value, 0, 64)
	f.field.SetUint(v)
	return err
}

func (f *field) setFloat(value string) error {
	v, err := strconv.ParseFloat(value, 64)
	f.field.SetFloat(v)
	return err
}

func (f *field) setSlice(value string) error {
	t := f.field.Type()
	setSliceElem := setSliceElem(t.Elem())

	if setSliceElem == nil {
		return nil
	}

	values := strings.Split(value, ",")
	valuesLen := len(values)

	f.field.Set(reflect.MakeSlice(t, valuesLen, valuesLen))

	for i, value := range values {
		err := setSliceElem(f.field.Index(i), strings.TrimSpace(value))
		if err != nil {
			return err
		}
	}

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
	f.SetInt(v)
	return err
}

func setSliceElemUint(f reflect.Value, value string) error {
	v, err := strconv.ParseUint(value, 0, 64)
	f.SetUint(v)
	return err
}

func setSliceElemFloat(f reflect.Value, value string) error {
	v, err := strconv.ParseFloat(value, 64)
	f.SetFloat(v)
	return err
}
