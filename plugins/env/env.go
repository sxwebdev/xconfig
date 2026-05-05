// Package env provides environment variables support for xconfig
package env

import (
	"encoding"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/internal/utils"
	"github.com/sxwebdev/xconfig/plugins"
)

const tag = "env"

func init() {
	plugins.RegisterTag(tag)
}

// New returns an EnvSet.
func New(prefix string) plugins.Plugin {
	return &visitor{
		prefix: prefix,
	}
}

type visitor struct {
	conf   any
	fields flat.Fields
	prefix string
}

func makeEnvName(prefix, name string) string {
	if prefix != "" {
		name = strings.ToUpper(prefix) + "_" + name
	}

	return name
}

// Walk captures the conf reference so Parse can re-flatten and expand
// slice/map fields based on env variables.
func (v *visitor) Walk(conf any) error {
	v.conf = conf
	return nil
}

func (v *visitor) Visit(f flat.Fields) error {
	v.fields = f

	for _, f := range v.fields {
		name, ok := f.Tag(tag)
		if !ok || name == "" {
			name = v.buildEnvName(f)
		} else {
			// If explicit tag is provided, still apply prefix
			name = makeEnvName(v.prefix, name)
		}

		f.Meta()[tag] = name
	}

	return nil
}

// buildEnvName constructs environment variable name considering parent struct tags
func (v *visitor) buildEnvName(f flat.Field) string {
	fieldName := f.Name()
	parts := strings.Split(fieldName, ".")

	if len(parts) == 1 {
		// Simple field without nesting
		return makeEnvName(v.prefix, f.EnvName())
	}

	// Check if parent struct has an env tag
	parentTag := f.ParentTag()
	if parentTag != "" {
		if parentEnvTag, ok := parentTag.Lookup(tag); ok && parentEnvTag != "" {
			// Build the env name using the parent's env tag as prefix.
			// Keep all path segments after the field whose tag we're using —
			// for slice/map paths the segments between the parent and the
			// leaf carry the index/key and must not be dropped.
			tail := parts[len(parts)-1]
			middle := parts[1 : len(parts)-1]
			segments := []string{parentEnvTag}
			for _, m := range middle {
				segments = append(segments, strings.ToUpper(strings.Join(utils.SplitNameByWords(m), "_")))
			}
			segments = append(segments, strings.ToUpper(toSnakeCase(tail)))
			return makeEnvName(v.prefix, strings.Join(segments, "_"))
		}
	}

	// No parent tag found, use default behavior
	return makeEnvName(v.prefix, f.EnvName())
}

// toSnakeCase converts PascalCase/camelCase to snake_case
func toSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result = append(result, '_')
		}
		result = append(result, r)
	}
	return string(result)
}

func (v *visitor) Parse() error {
	// Expand slices and maps based on env-vars BEFORE we re-flatten.
	// We compute env names ourselves during the walk, so we don't depend on
	// flat.Field.EnvName for slice/map elements.
	nameMap := make(map[string]string, 32)
	if v.conf != nil {
		if err := v.walkAndExpand(reflect.ValueOf(v.conf), "", "", false, nameMap); err != nil {
			return err
		}
	}

	// Re-flatten conf — picks up slice/map entries we just created.
	fields, err := flat.View(v.conf)
	if err != nil {
		return err
	}

	// Re-stamp env names on fields (including newly created ones). For fields
	// known to nameMap (slice/map entries) use the precomputed name; for the
	// rest fall back to the existing logic so explicit env tags still win.
	for _, f := range fields {
		name, ok := nameMap[f.Name()]
		if !ok {
			tagName, hasTag := f.Tag(tag)
			if hasTag && tagName != "" {
				name = makeEnvName(v.prefix, tagName)
			} else {
				name = v.buildEnvName(f)
			}
		}
		f.Meta()[tag] = name
	}
	v.fields = fields

	for _, f := range v.fields {
		name, ok := f.Meta()[tag]
		if !ok || name == "-" {
			continue
		}

		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}

		if err := f.Set(value); err != nil {
			return err
		}
	}

	return nil
}

// walkAndExpand walks conf reflectively, expanding slice-of-struct and map
// fields based on os.Environ. For every leaf scalar (or primitive slice) it
// records the computed env-var name in nameMap keyed by the field's flat path.
//
// pathPrefix mirrors flat.View's path naming (struct-field names for plain
// nesting, ".N" for slice indices, ".key" for map entries).
// envPrefix is the env-var name accumulated so far (without the leaf segment).
// inContainer is true once we are inside a slice element or map value, so a
// field-level `env:` tag is treated as a segment override (relative to
// envPrefix) instead of an absolute name override.
func (v *visitor) walkAndExpand(rs reflect.Value, pathPrefix, envPrefix string, inContainer bool, nameMap map[string]string) error {
	for rs.Kind() == reflect.Ptr || rs.Kind() == reflect.Interface {
		if rs.IsNil() {
			return nil
		}
		rs = rs.Elem()
	}
	if rs.Kind() != reflect.Struct {
		return nil
	}

	ts := rs.Type()
	for i := 0; i < rs.NumField(); i++ {
		fv := rs.Field(i)
		ft := ts.Field(i)

		if !ft.IsExported() {
			continue
		}

		fieldPath := pathPrefix
		if !ft.Anonymous {
			if fieldPath == "" {
				fieldPath = ft.Name
			} else {
				fieldPath = fieldPath + "." + ft.Name
			}
		}

		fieldEnv := computeFieldEnv(envPrefix, ft, v.prefix, inContainer)

		switch fv.Kind() {
		case reflect.Struct:
			if err := v.walkAndExpand(fv, fieldPath, fieldEnv, inContainer, nameMap); err != nil {
				return err
			}

		case reflect.Slice:
			elemType := fv.Type().Elem()
			isPtr := elemType.Kind() == reflect.Ptr
			innerType := elemType
			if isPtr {
				innerType = innerType.Elem()
			}

			// Primitive-element slices (or slices of TextUnmarshaler) are
			// handled as a single leaf — Field.Set parses comma-separated.
			if innerType.Kind() != reflect.Struct || implementsTextUnmarshaler(elemType) {
				nameMap[fieldPath] = fieldEnv
				continue
			}

			// Slice of struct (or *struct) — grow based on env-var indices.
			maxIdx := scanSliceMaxIndex(fieldEnv)
			curLen := fv.Len()
			if maxIdx >= 0 && maxIdx+1 > curLen {
				target := maxIdx + 1
				newSlice := reflect.MakeSlice(fv.Type(), target, target)
				reflect.Copy(newSlice, fv)
				if isPtr {
					for j := curLen; j < target; j++ {
						newSlice.Index(j).Set(reflect.New(innerType))
					}
				}
				fv.Set(newSlice)
			}

			for j := 0; j < fv.Len(); j++ {
				elem := fv.Index(j)
				if elem.Kind() == reflect.Ptr {
					if elem.IsNil() {
						elem.Set(reflect.New(innerType))
					}
					elem = elem.Elem()
				}
				idxPath := fieldPath + "." + strconv.Itoa(j)
				idxEnv := fieldEnv + "_" + strconv.Itoa(j)
				if err := v.walkAndExpand(elem, idxPath, idxEnv, true, nameMap); err != nil {
					return err
				}
			}

		case reflect.Map:
			if fv.Type().Key().Kind() != reflect.String {
				continue
			}
			elemType := fv.Type().Elem()
			isPtr := elemType.Kind() == reflect.Ptr
			innerType := elemType
			if isPtr {
				innerType = innerType.Elem()
			}
			structValue := innerType.Kind() == reflect.Struct && !implementsTextUnmarshaler(elemType)

			if !structValue {
				if err := v.expandPrimitiveMap(fv, fieldPath, fieldEnv, nameMap); err != nil {
					return err
				}
				continue
			}

			if err := v.expandStructMap(fv, fieldPath, fieldEnv, innerType, isPtr, nameMap); err != nil {
				return err
			}

		default:
			nameMap[fieldPath] = fieldEnv
		}
	}
	return nil
}

// computeFieldEnv computes the env-var prefix for a struct field given the
// running envPrefix accumulator. An `env:` tag at the top level anchors the
// full env name (prefixed only with the global plugin prefix). Inside a slice
// or map element it acts as a per-segment override so the surrounding
// index/key isn't dropped (otherwise different elements would collide on the
// same env var).
func computeFieldEnv(envPrefix string, ft reflect.StructField, globalPrefix string, inContainer bool) string {
	if envTag, ok := ft.Tag.Lookup(tag); ok && envTag != "" {
		if inContainer && envPrefix != "" {
			return envPrefix + "_" + envTag
		}
		return makeEnvName(globalPrefix, envTag)
	}

	if ft.Anonymous {
		// Embedded struct doesn't add a path segment.
		if envPrefix == "" {
			return makeEnvName(globalPrefix, "")
		}
		return envPrefix
	}

	seg := strings.ToUpper(strings.Join(utils.SplitNameByWords(ft.Name), "_"))
	if envPrefix == "" {
		return makeEnvName(globalPrefix, seg)
	}
	return envPrefix + "_" + seg
}

func (v *visitor) expandPrimitiveMap(fv reflect.Value, fieldPath, fieldEnv string, nameMap map[string]string) error {
	if fv.IsNil() {
		fv.Set(reflect.MakeMap(fv.Type()))
	}

	keyType := fv.Type().Key()
	envPrefix := fieldEnv + "_"

	for _, env := range os.Environ() {
		eqIdx := strings.IndexByte(env, '=')
		if eqIdx < 0 {
			continue
		}
		key := env[:eqIdx]
		if !strings.HasPrefix(key, envPrefix) {
			continue
		}
		mapKey := key[len(envPrefix):]
		if mapKey == "" {
			continue
		}
		keyVal := reflect.ValueOf(mapKey).Convert(keyType)
		if !fv.MapIndex(keyVal).IsValid() {
			fv.SetMapIndex(keyVal, reflect.Zero(fv.Type().Elem()))
		}
	}

	iter := fv.MapRange()
	for iter.Next() {
		k := iter.Key().String()
		nameMap[fieldPath+"."+k] = fieldEnv + "_" + k
	}
	return nil
}

func (v *visitor) expandStructMap(fv reflect.Value, fieldPath, fieldEnv string, innerType reflect.Type, isPtr bool, nameMap map[string]string) error {
	if fv.IsNil() {
		fv.Set(reflect.MakeMap(fv.Type()))
	}

	suffixes := enumerateLeafSuffixes(innerType)

	envPrefix := fieldEnv + "_"
	for _, env := range os.Environ() {
		eqIdx := strings.IndexByte(env, '=')
		if eqIdx < 0 {
			continue
		}
		key := env[:eqIdx]
		if !strings.HasPrefix(key, envPrefix) {
			continue
		}
		suffix := key[len(envPrefix):]

		// Try to match suffix as <KEY>_<FIELD_TAIL> — try longest field tail first.
		mapKey := ""
		for _, sfx := range suffixes {
			if sfx == "" {
				continue
			}
			if suffix == sfx {
				// suffix is exactly a leaf — that means an empty map key,
				// which we don't support.
				continue
			}
			if strings.HasSuffix(suffix, "_"+sfx) {
				mapKey = strings.TrimSuffix(suffix, "_"+sfx)
				break
			}
		}
		if mapKey == "" {
			continue
		}

		keyVal := reflect.ValueOf(mapKey).Convert(fv.Type().Key())
		if fv.MapIndex(keyVal).IsValid() {
			continue
		}
		if isPtr {
			fv.SetMapIndex(keyVal, reflect.New(innerType))
		} else {
			fv.SetMapIndex(keyVal, reflect.New(innerType).Elem())
		}
	}

	// For each existing map entry (including ones added above and ones
	// preloaded from a YAML file etc.), compute env names for inner fields.
	// Use a stand-in zero value of the struct type — only the type info matters.
	scratch := reflect.New(innerType).Elem()
	iter := fv.MapRange()
	for iter.Next() {
		k := iter.Key().String()
		entryPath := fieldPath + "." + k
		entryEnv := fieldEnv + "_" + k
		if err := v.walkAndExpand(scratch, entryPath, entryEnv, true, nameMap); err != nil {
			return err
		}
	}
	return nil
}

// enumerateLeafSuffixes returns env-name suffixes for leaf fields of t,
// excluding nested slice/map fields. Used to find <KEY>_<FIELD_TAIL> splits
// when expanding map[string]struct from env vars.
func enumerateLeafSuffixes(t reflect.Type) []string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	var suffixes []string
	collectLeafSuffixes(t, "", &suffixes)
	// Sort by length descending so the longest match wins when a suffix is a
	// substring of another (e.g. "PORT" vs "DEFAULT_PORT").
	for i := 0; i < len(suffixes)-1; i++ {
		for j := i + 1; j < len(suffixes); j++ {
			if len(suffixes[j]) > len(suffixes[i]) {
				suffixes[i], suffixes[j] = suffixes[j], suffixes[i]
			}
		}
	}
	return suffixes
}

func collectLeafSuffixes(t reflect.Type, prefix string, out *[]string) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		var seg string
		if envTag, ok := f.Tag.Lookup(tag); ok && envTag != "" {
			// Inside a map element env tags act as a per-segment override —
			// match the relative semantics in computeFieldEnv (inContainer).
			seg = envTag
		} else if !f.Anonymous {
			seg = strings.ToUpper(strings.Join(utils.SplitNameByWords(f.Name), "_"))
		}

		var newPrefix string
		switch {
		case seg == "":
			newPrefix = prefix
		case prefix == "":
			newPrefix = seg
		default:
			newPrefix = prefix + "_" + seg
		}

		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		switch ft.Kind() {
		case reflect.Struct:
			if implementsTextUnmarshaler(f.Type) {
				*out = append(*out, newPrefix)
				continue
			}
			collectLeafSuffixes(ft, newPrefix, out)
		case reflect.Slice, reflect.Map:
			// Primitive slice (or anything Field.Set can parse) — single env name.
			elem := ft.Elem()
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct && !implementsTextUnmarshaler(ft.Elem()) {
				// Nested slice/map of struct inside a struct-valued map —
				// not supported for env-driven expansion.
				continue
			}
			*out = append(*out, newPrefix)
		default:
			*out = append(*out, newPrefix)
		}
	}
}

// scanSliceMaxIndex looks at os.Environ for keys matching prefix_<N> or
// prefix_<N>_... and returns the largest non-negative N. Returns -1 if none.
func scanSliceMaxIndex(prefix string) int {
	if prefix == "" {
		return -1
	}
	wanted := prefix + "_"
	max := -1
	for _, env := range os.Environ() {
		eqIdx := strings.IndexByte(env, '=')
		if eqIdx < 0 {
			continue
		}
		key := env[:eqIdx]
		if !strings.HasPrefix(key, wanted) {
			continue
		}
		rest := key[len(wanted):]
		// First segment up to next '_' (or end of string) must be a non-negative integer.
		end := strings.IndexByte(rest, '_')
		var idxStr string
		if end < 0 {
			idxStr = rest
		} else {
			idxStr = rest[:end]
		}
		n, err := strconv.Atoi(idxStr)
		if err != nil || n < 0 {
			continue
		}
		if n > max {
			max = n
		}
	}
	return max
}

var textUnmarshalerType = reflect.TypeOf(new(encoding.TextUnmarshaler)).Elem()

func implementsTextUnmarshaler(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return true
	}
	return false
}
