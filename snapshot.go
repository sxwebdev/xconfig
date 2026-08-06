package xconfig

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"
	"unsafe"
)

const sharedSnapshotTag = "xconfig_shared"

// sharedPointerTypes are the stdlib pointer types that are immutable, safe for
// concurrent use, and routinely compared by identity, so a snapshot keeps the
// caller's pointer. Every other pointer is owned by the snapshot, including
// pointers to types without exported fields such as *big.Int or *bytes.Buffer,
// whose contents a refresh mutates in place. Application dependencies opt into
// sharing with xconfig_shared:"true".
var sharedPointerTypes = map[reflect.Type]struct{}{
	reflect.TypeFor[*time.Location](): {},
	reflect.TypeFor[*regexp.Regexp](): {},
}

var (
	ErrNotParsed             = errors.New("xconfig: configuration has not been parsed")
	ErrNilConfig             = errors.New("xconfig: config is nil")
	ErrInvalidSnapshotTarget = errors.New("xconfig: snapshot destination must be a non-nil pointer")
)

func Snapshot[T any](c Config) (T, error) {
	var dst T
	if isNilConfig(c) {
		return dst, ErrNilConfig
	}
	if err := c.Snapshot(&dst); err != nil {
		return dst, err
	}
	return dst, nil
}

func (c *config) Snapshot(dst any) error {
	if c == nil {
		return ErrNilConfig
	}
	if err := validateDestination(dst); err != nil {
		return err
	}

	current := c.currentSnapshot()
	if current == nil {
		return ErrNotParsed
	}
	return copyConfig(dst, current)
}

func cloneConfigPointer(src any) (any, error) {
	if err := validateDestination(src); err != nil {
		return nil, err
	}

	v := reflect.ValueOf(src)
	clone, err := cloneReflectValue(v)
	if err != nil {
		return nil, err
	}
	return clone.Interface(), nil
}

func copyConfig(dst, src any) error {
	if err := validateDestination(dst); err != nil {
		return err
	}

	dstValue := reflect.ValueOf(dst)
	srcValue := reflect.ValueOf(src)
	if srcValue.Kind() != reflect.Pointer || srcValue.IsNil() || dstValue.Type() != srcValue.Type() {
		return fmt.Errorf("xconfig: snapshot destination has type %s, want %s", dstValue.Type(), srcValue.Type())
	}

	clone, err := cloneReflectValue(srcValue.Elem())
	if err != nil {
		return err
	}
	dstValue.Elem().Set(clone)
	return nil
}

func cloneReflectValue(src reflect.Value) (clone reflect.Value, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			clone = reflect.Value{}
			err = fmt.Errorf("xconfig: clone configuration: %v", recovered)
		}
	}()
	return cloneValue(src, make(map[cloneVisit]reflect.Value), false), nil
}

func validateDestination(dst any) error {
	if dst == nil {
		return ErrInvalidSnapshotTarget
	}
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return ErrInvalidSnapshotTarget
	}
	return nil
}

func isNilConfig(c Config) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

type cloneVisit struct {
	typ  reflect.Type
	ptr  uintptr
	kind reflect.Kind
	len  int
	cap  int
}

// cloneValue copies src. interned reports whether src was reached through a
// struct without exported fields, that is, through a value type whose whole
// state is private. Pointers found there are the interned or sentinel payloads
// that give time.Time, netip.Addr and unique.Handle their value semantics, so
// they are shared rather than cloned; slices and maps found there are still
// cloned, so a snapshot owns the storage behind values such as big.Int and
// bytes.Buffer.
func cloneValue(src reflect.Value, seen map[cloneVisit]reflect.Value, interned bool) reflect.Value { //nolint:gocyclo
	if !src.IsValid() {
		return src
	}
	src = addressableValue(src)

	switch src.Kind() {
	case reflect.Pointer:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		if interned {
			return src
		}
		if _, ok := sharedPointerTypes[src.Type()]; ok {
			return src
		}
		key := cloneVisit{typ: src.Type(), ptr: src.Pointer(), kind: src.Kind()}
		if previous, ok := seen[key]; ok {
			return previous
		}
		dst := reflect.New(src.Type().Elem())
		seen[key] = dst
		dst.Elem().Set(cloneValue(src.Elem(), seen, false))
		return dst

	case reflect.Interface:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		dst := reflect.New(src.Type()).Elem()
		dst.Set(cloneValue(src.Elem(), seen, interned))
		return dst

	case reflect.Struct:
		interned = interned || isOpaqueStruct(src.Type())
		dst := reflect.New(src.Type()).Elem()
		for i := 0; i < src.NumField(); i++ {
			fieldType := src.Type().Field(i)
			srcField := accessibleValue(src.Field(i))
			dstField := accessibleValue(dst.Field(i))
			if fieldType.Tag.Get(sharedSnapshotTag) == "true" {
				dstField.Set(srcField)
				continue
			}
			dstField.Set(cloneValue(srcField, seen, interned))
		}
		return dst

	case reflect.Slice:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		key := cloneVisit{typ: src.Type(), ptr: src.Pointer(), kind: src.Kind(), len: src.Len(), cap: src.Cap()}
		if previous, ok := seen[key]; ok {
			return previous
		}
		dst := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		seen[key] = dst
		for i := 0; i < src.Len(); i++ {
			dst.Index(i).Set(cloneValue(src.Index(i), seen, interned))
		}
		return dst

	case reflect.Map:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		key := cloneVisit{typ: src.Type(), ptr: src.Pointer(), kind: src.Kind()}
		if previous, ok := seen[key]; ok {
			return previous
		}
		dst := reflect.MakeMapWithSize(src.Type(), src.Len())
		seen[key] = dst
		iter := src.MapRange()
		for iter.Next() {
			value := cloneValue(iter.Value(), seen, interned)
			dst.SetMapIndex(cloneValue(iter.Key(), seen, interned), value)
		}
		return dst

	case reflect.Array:
		dst := reflect.New(src.Type()).Elem()
		for i := 0; i < src.Len(); i++ {
			dst.Index(i).Set(cloneValue(src.Index(i), seen, interned))
		}
		return dst

	default:
		return src
	}
}

// isOpaqueStruct reports whether t is a struct type without exported fields.
// Such a value keeps every byte of its state private, so the pointers it holds
// are private payloads rather than reachable config data.
func isOpaqueStruct(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			return false
		}
	}
	return true
}

type comparePair struct {
	typ reflect.Type
	a   uintptr
	b   uintptr
}

// sameConfigData reports whether two configurations hold the same container
// shape and configuration data. It backs the refresh publish gate, so it
// deliberately compares only what a plugin could have changed: fields tagged
// xconfig_shared:"true" hold the same value in both copies by construction,
// unexported state is private to the caller and never written by a plugin, and
// func fields cannot be set from a string yet never compare equal, so all three
// are skipped instead of forcing a publication on every cycle.
func sameConfigData(a, b any) bool {
	return sameValue(reflect.ValueOf(a), reflect.ValueOf(b), make(map[comparePair]struct{}))
}

func sameValue(a, b reflect.Value, seen map[comparePair]struct{}) bool { //nolint:gocyclo
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}

	switch a.Kind() {
	case reflect.Func:
		return true

	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		if a.Pointer() == b.Pointer() || !firstVisit(seen, a, b) {
			return true
		}
		return sameValue(a.Elem(), b.Elem(), seen)

	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return sameValue(a.Elem(), b.Elem(), seen)

	case reflect.Struct:
		if isOpaqueStruct(a.Type()) {
			// Copied without recursing into private state, so the whole value
			// is either comparable or beyond a plugin's reach.
			return !a.Type().Comparable() || a.Equal(b)
		}
		for i := 0; i < a.NumField(); i++ {
			fieldType := a.Type().Field(i)
			if !fieldType.IsExported() || fieldType.Tag.Get(sharedSnapshotTag) == "true" {
				continue
			}
			if !sameValue(a.Field(i), b.Field(i), seen) {
				return false
			}
		}
		return true

	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		if a.Len() == 0 || a.Pointer() == b.Pointer() || !firstVisit(seen, a, b) {
			return true
		}
		return sameElements(a, b, seen)

	case reflect.Array:
		return sameElements(a, b, seen)

	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		if a.Len() == 0 || a.Pointer() == b.Pointer() || !firstVisit(seen, a, b) {
			return true
		}
		return sameMapEntries(a, b, seen)

	default:
		return a.Equal(b)
	}
}

func sameElements(a, b reflect.Value, seen map[comparePair]struct{}) bool {
	for i := 0; i < a.Len(); i++ {
		if !sameValue(a.Index(i), b.Index(i), seen) {
			return false
		}
	}
	return true
}

func sameMapEntries(a, b reflect.Value, seen map[comparePair]struct{}) bool {
	// A copy owns its keys, so a key holding a pointer is a different key there
	// and cannot be looked up. Such maps are compared by entry count only.
	comparableKeys := clonedKeyKeepsIdentity(a.Type().Key())
	iter := a.MapRange()
	for iter.Next() {
		other := b.MapIndex(iter.Key())
		if !other.IsValid() {
			if comparableKeys {
				return false
			}
			continue
		}
		if !sameValue(iter.Value(), other, seen) {
			return false
		}
	}
	return true
}

// clonedKeyKeepsIdentity reports whether a map key of type t still equals its
// original after being copied into a snapshot.
func clonedKeyKeepsIdentity(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface:
		_, shared := sharedPointerTypes[t]
		return shared

	case reflect.Struct:
		if isOpaqueStruct(t) {
			return true
		}
		for i := 0; i < t.NumField(); i++ {
			if !clonedKeyKeepsIdentity(t.Field(i).Type) {
				return false
			}
		}
		return true

	case reflect.Array:
		return clonedKeyKeepsIdentity(t.Elem())

	default:
		return true
	}
}

// firstVisit records a pair of containers and reports whether it is new, so
// that a self-referencing configuration terminates.
func firstVisit(seen map[comparePair]struct{}, a, b reflect.Value) bool {
	pair := comparePair{typ: a.Type(), a: a.Pointer(), b: b.Pointer()}
	if _, ok := seen[pair]; ok {
		return false
	}
	seen[pair] = struct{}{}
	return true
}

func addressableValue(value reflect.Value) reflect.Value {
	if !value.CanAddr() {
		addressable := reflect.New(value.Type()).Elem()
		addressable.Set(value)
		value = addressable
	}
	return accessibleValue(value)
}

// accessibleValue removes reflect's package visibility restriction so mutable
// unexported fields do not remain shallow aliases between snapshots. The value
// must be addressable when it is not already accessible.
func accessibleValue(value reflect.Value) reflect.Value {
	if value.CanInterface() || !value.CanAddr() {
		return value
	}
	return reflect.NewAt(value.Type(), unsafe.Pointer(value.UnsafeAddr())).Elem()
}
