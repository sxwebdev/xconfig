package flat

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/sxwebdev/xconfig/internal/utils"
)

// ExpandContainersFromKeys mutates conf, growing slice-of-struct fields (by
// numeric index) and maps (by key) based on the provided UPPER_SNAKE_CASE
// keys. The same convention used by the env plugin applies — keys for slice
// elements look like <PREFIX>_<N>_<TAIL>; for primitive maps <PREFIX>_<KEY>;
// for struct-valued maps <PREFIX>_<KEY>_<TAIL> where TAIL matches one of the
// inner struct's leaf env names (longest match wins).
//
// Pointer element types ([]*T, map[string]*T) are supported — empty slots
// are allocated as &T{}.
//
// Returns a map from each leaf field's flat path (e.g. "Items.0.Host") to
// its computed env-style name (e.g. "ITEMS_0_HOST"). Both env and Vault
// plugins use this to look up values by env-style name after re-flattening
// the (now expanded) conf.
//
// globalPrefix is uppercased and prepended (with an underscore) to all
// top-level field env names. Pass "" if not using a prefix.
func ExpandContainersFromKeys(conf any, globalPrefix string, keys []string) (map[string]string, error) {
	nameMap := make(map[string]string, 32)
	if conf == nil {
		return nameMap, nil
	}
	if err := walkAndExpand(reflect.ValueOf(conf), "", "", false, globalPrefix, keys, nameMap); err != nil {
		return nil, err
	}
	return nameMap, nil
}

const envTagName = "env"

// MakeEnvName prepends globalPrefix (uppercased) to name with an underscore
// separator, matching the convention used by ExpandContainersFromKeys.
func MakeEnvName(globalPrefix, name string) string {
	if globalPrefix != "" {
		return strings.ToUpper(globalPrefix) + "_" + name
	}
	return name
}

func walkAndExpand(rs reflect.Value, pathPrefix, envPrefix string, inContainer bool, globalPrefix string, keys []string, nameMap map[string]string) error {
	for rs.Kind() == reflect.Pointer || rs.Kind() == reflect.Interface {
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

		fieldEnv := computeFieldEnv(envPrefix, ft, globalPrefix, inContainer)

		switch fv.Kind() {
		case reflect.Struct:
			if err := walkAndExpand(fv, fieldPath, fieldEnv, inContainer, globalPrefix, keys, nameMap); err != nil {
				return err
			}

		case reflect.Slice:
			elemType := fv.Type().Elem()
			isPtr := elemType.Kind() == reflect.Pointer
			innerType := elemType
			if isPtr {
				innerType = innerType.Elem()
			}

			if innerType.Kind() != reflect.Struct || implementsTextUnmarshaler(elemType) {
				nameMap[fieldPath] = fieldEnv
				continue
			}

			maxIdx := scanSliceMaxIndex(fieldEnv, keys)
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
				if elem.Kind() == reflect.Pointer {
					if elem.IsNil() {
						elem.Set(reflect.New(innerType))
					}
					elem = elem.Elem()
				}
				idxPath := fieldPath + "." + strconv.Itoa(j)
				idxEnv := fieldEnv + "_" + strconv.Itoa(j)
				if err := walkAndExpand(elem, idxPath, idxEnv, true, globalPrefix, keys, nameMap); err != nil {
					return err
				}
			}

		case reflect.Map:
			if fv.Type().Key().Kind() != reflect.String {
				continue
			}
			elemType := fv.Type().Elem()
			isPtr := elemType.Kind() == reflect.Pointer
			innerType := elemType
			if isPtr {
				innerType = innerType.Elem()
			}
			structValue := innerType.Kind() == reflect.Struct && !implementsTextUnmarshaler(elemType)

			if !structValue {
				if err := expandPrimitiveMap(fv, fieldPath, fieldEnv, keys, nameMap); err != nil {
					return err
				}
				continue
			}

			if err := expandStructMap(fv, fieldPath, fieldEnv, innerType, isPtr, globalPrefix, keys, nameMap); err != nil {
				return err
			}

		default:
			nameMap[fieldPath] = fieldEnv
		}
	}
	return nil
}

func computeFieldEnv(envPrefix string, ft reflect.StructField, globalPrefix string, inContainer bool) string {
	if envTag, ok := ft.Tag.Lookup(envTagName); ok && envTag != "" {
		if inContainer && envPrefix != "" {
			return envPrefix + "_" + envTag
		}
		return MakeEnvName(globalPrefix, envTag)
	}

	if ft.Anonymous {
		if envPrefix == "" {
			return MakeEnvName(globalPrefix, "")
		}
		return envPrefix
	}

	seg := strings.ToUpper(strings.Join(utils.SplitNameByWords(ft.Name), "_"))
	if envPrefix == "" {
		return MakeEnvName(globalPrefix, seg)
	}
	return envPrefix + "_" + seg
}

func expandPrimitiveMap(fv reflect.Value, fieldPath, fieldEnv string, keys []string, nameMap map[string]string) error {
	if fv.IsNil() {
		fv.Set(reflect.MakeMap(fv.Type()))
	}

	keyType := fv.Type().Key()
	envPrefix := fieldEnv + "_"

	for _, key := range keys {
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

func expandStructMap(fv reflect.Value, fieldPath, fieldEnv string, innerType reflect.Type, isPtr bool, globalPrefix string, keys []string, nameMap map[string]string) error {
	if fv.IsNil() {
		fv.Set(reflect.MakeMap(fv.Type()))
	}

	suffixes := enumerateLeafSuffixes(innerType)

	envPrefix := fieldEnv + "_"
	for _, key := range keys {
		if !strings.HasPrefix(key, envPrefix) {
			continue
		}
		suffix := key[len(envPrefix):]

		mapKey := ""
		for _, sfx := range suffixes {
			if sfx == "" {
				continue
			}
			if suffix == sfx {
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

	scratch := reflect.New(innerType).Elem()
	iter := fv.MapRange()
	for iter.Next() {
		k := iter.Key().String()
		entryPath := fieldPath + "." + k
		entryEnv := fieldEnv + "_" + k
		if err := walkAndExpand(scratch, entryPath, entryEnv, true, globalPrefix, keys, nameMap); err != nil {
			return err
		}
	}
	return nil
}

func enumerateLeafSuffixes(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	var suffixes []string
	collectLeafSuffixes(t, "", &suffixes)
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
	for t.Kind() == reflect.Pointer {
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
		if envTag, ok := f.Tag.Lookup(envTagName); ok && envTag != "" {
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
		for ft.Kind() == reflect.Pointer {
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
			elem := ft.Elem()
			for elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct && !implementsTextUnmarshaler(ft.Elem()) {
				continue
			}
			*out = append(*out, newPrefix)
		default:
			*out = append(*out, newPrefix)
		}
	}
}

func scanSliceMaxIndex(prefix string, keys []string) int {
	if prefix == "" {
		return -1
	}
	wanted := prefix + "_"
	max := -1
	for _, key := range keys {
		if !strings.HasPrefix(key, wanted) {
			continue
		}
		rest := key[len(wanted):]
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

func implementsTextUnmarshaler(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return true
	}
	return false
}
