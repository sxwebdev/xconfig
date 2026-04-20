// Package customdefaults provides custom default values for xconfig.
//
// Types that implement SetDefaults() get it called by the plugin. The plugin
// walks the whole config graph and invokes SetDefaults on every reachable
// struct whose pointer receiver implements the interface (including elements
// of slices, arrays, and values of maps). Children are visited first, so a
// parent's SetDefaults observes (and can override) values populated by its
// children.
package customdefaults

import (
	"reflect"

	"github.com/sxwebdev/xconfig/plugins"
)

type setCustomDefaults interface {
	SetDefaults()
}

// New returns a customdefaults plugin.
func New() plugins.Plugin {
	return &visitor{}
}

type visitor struct {
	config any
}

func (v *visitor) Parse() error {
	if v.config == nil {
		return nil
	}

	rv := reflect.ValueOf(v.config)
	walkAndSetDefaults(rv, map[uintptr]struct{}{})
	return nil
}

func (v *visitor) Walk(config any) error {
	v.config = config
	return nil
}

// walkAndSetDefaults visits every reachable struct in rv, invoking
// SetDefaults() on the pointer receiver if it implements the interface.
// Children are visited first so a parent's SetDefaults sees child values.
func walkAndSetDefaults(rv reflect.Value, visited map[uintptr]struct{}) {
	if !rv.IsValid() {
		return
	}

	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return
		}
		if rv.Kind() == reflect.Ptr {
			ptr := rv.Pointer()
			if _, seen := visited[ptr]; seen {
				return
			}
			visited[ptr] = struct{}{}
		}
		walkAndSetDefaults(rv.Elem(), visited)

	case reflect.Struct:
		for i := 0; i < rv.NumField(); i++ {
			ft := rv.Type().Field(i)
			if !ft.IsExported() {
				continue
			}
			walkAndSetDefaults(rv.Field(i), visited)
		}
		invokeSetDefaults(rv)

	case reflect.Slice, reflect.Array:
		elemType := rv.Type().Elem()
		if !containsStruct(elemType) {
			return
		}
		for i := 0; i < rv.Len(); i++ {
			walkAndSetDefaults(rv.Index(i), visited)
		}

	case reflect.Map:
		elemType := rv.Type().Elem()
		if !containsStruct(elemType) {
			return
		}
		keys := rv.MapKeys()
		for _, key := range keys {
			val := rv.MapIndex(key)

			// If the element is a pointer or anything else already addressable
			// via the map, we can walk it directly; but map values aren't
			// addressable, so for struct values we must work on a copy and
			// write back.
			if val.Kind() == reflect.Ptr {
				walkAndSetDefaults(val, visited)
				continue
			}

			if val.Kind() == reflect.Struct {
				addressable := reflect.New(val.Type()).Elem()
				addressable.Set(val)
				walkAndSetDefaults(addressable, visited)
				rv.SetMapIndex(key, addressable)
			}
		}
	}
}

// invokeSetDefaults calls SetDefaults on rv's pointer receiver when
// possible, falling back to a direct call if the value itself implements
// the interface with a value receiver.
func invokeSetDefaults(rv reflect.Value) {
	if rv.CanAddr() {
		if s, ok := rv.Addr().Interface().(setCustomDefaults); ok {
			s.SetDefaults()
			return
		}
	}
	if rv.CanInterface() {
		if s, ok := rv.Interface().(setCustomDefaults); ok {
			s.SetDefaults()
		}
	}
}

// containsStruct reports whether t is a struct type or (recursively) a
// pointer/slice/array/map whose element type is a struct. Used to prune
// traversal of primitive containers.
func containsStruct(t reflect.Type) bool {
	for {
		switch t.Kind() {
		case reflect.Struct:
			return true
		case reflect.Ptr, reflect.Slice, reflect.Array:
			t = t.Elem()
		case reflect.Map:
			t = t.Elem()
		default:
			return false
		}
	}
}
