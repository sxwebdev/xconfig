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

// Decoder парсит .env и раскладывает значения в произвольную Go-структуру.
type Decoder struct{}

// New создаёт новый Decoder.
func New() *Decoder { return &Decoder{} }

// Format возвращает формат декодера.
func (d *Decoder) Format() string {
	return "env"
}

// Unmarshal разбирает []byte (формат .env) и заполняет v – указатель на struct.
func (d *Decoder) Unmarshal(data []byte, v any) error {
	// 1) Распарсить .env → map[string]string
	flatMap, err := godotenv.UnmarshalBytes(data)
	if err != nil {
		return err
	}

	// 2) Проверяем, что v – непустой указатель на struct
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("xconfigdotenv: Unmarshal: v must be a non-nil pointer to a struct, got %T", v)
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("xconfigdotenv: Unmarshal: v must point to a struct, got pointer to %s", elem.Kind())
	}

	// 3) Для каждого ключа из .env разбираем строку в нужное поле
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

// assignValue пытается положить rawVal (строку) в поле v (reflect.Value of a struct)
func assignValue(v reflect.Value, parts []string, rawVal string) error {
	typ := v.Type()

	// Перебираем все префиксы от полного к минимальному
	for prefixLen := len(parts); prefixLen >= 1; prefixLen-- {
		prefixJoined := strings.Join(parts[:prefixLen], "_")
		normalizedPrefix := normalize(prefixJoined)

		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			// normalize имени поля и имени его типа
			fieldNameNorm := normalize(field.Name)
			fieldTypeNameNorm := normalize(field.Type.Name())

			// если ни имя поля, ни имя его типа не совпадают с normalizedPrefix, пропускаем
			if fieldNameNorm != normalizedPrefix && fieldTypeNameNorm != normalizedPrefix {
				continue
			}

			// Нашли подходящее поле - получаем его через unsafe для работы с приватными полями
			fieldVal := getFieldValue(v, i)
			leftover := parts[prefixLen:] // сегменты «после» текущего префикса

			// 1) Если leftover пустой, это «конечное» поле: базовый тип или указатель на базовый
			if len(leftover) == 0 {
				return setBasicValue(fieldVal, rawVal)
			}

			// 2) Иначе нужно «спуститься» или положить в контейнер
			switch fieldVal.Kind() {
			case reflect.Ptr:
				// Указатель: если nil – создаём новый; затем ожидаем struct и рекурсивно спускаемся
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
				// Вложенная структура – рекурсивно спускаемся
				return assignValue(fieldVal, leftover, rawVal)

			case reflect.Map:
				// Map: leftover объединяем, получаем ключ; rawVal – значение
				if len(leftover) == 0 {
					return fmt.Errorf("map field %q but no key given (leftover is empty)", field.Name)
				}
				if fieldVal.IsNil() { // инициализируем, если нужно
					newMap := reflect.MakeMap(fieldVal.Type())
					if err := setWithReflect(fieldVal, newMap); err != nil {
						return err
					}
				}
				mapKey := strings.Join(leftover, "_")
				return setMapValue(fieldVal, mapKey, rawVal)

			case reflect.Slice:
				// Срез: leftover[0] – индекс (число), leftover[1:] – вложенность внутри элемента (если есть)
				idxStr := leftover[0]
				ix, err := strconv.Atoi(idxStr)
				if err != nil {
					return fmt.Errorf("cannot parse slice index %q for field %q", idxStr, field.Name)
				}
				// Если срез nil – инициализируем пустой
				if fieldVal.IsNil() {
					newSlice := reflect.MakeSlice(fieldVal.Type(), 0, 0)
					if err := setWithReflect(fieldVal, newSlice); err != nil {
						return err
					}
				}
				// Расширяем срез если нужно
				curLen := fieldVal.Len()
				if ix >= curLen {
					newLen := ix + 1
					newSlice := reflect.MakeSlice(fieldVal.Type(), newLen, newLen)
					// Копируем элементы в новый срез
					for j := 0; j < curLen; j++ {
						elem := fieldVal.Index(j)
						target := newSlice.Index(j)
						setWithReflect(target, elem)
					}
					if err := setWithReflect(fieldVal, newSlice); err != nil {
						return err
					}
				}
				// Достаём элемент
				elemVal := fieldVal.Index(ix)
				// Если после индекса есть вложенность
				if len(leftover) > 1 {
					switch elemVal.Kind() {
					case reflect.Ptr:
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
				// Иначе – просто базовое присваивание в элемент
				return setBasicValue(elemVal, rawVal)

			default:
				// Не контейнер, но leftover есть – некорректное вложение
				return fmt.Errorf("cannot descend into field %q (kind %s), leftover %v", field.Name, fieldVal.Kind(), leftover)
			}
		}
	}

	// Ни один префикс не нашёлся – просто игнорируем этот ключ
	return nil
}

// getFieldValue получает значение поля по индексу с поддержкой приватных полей через unsafe
func getFieldValue(structVal reflect.Value, fieldIndex int) reflect.Value {
	field := structVal.Field(fieldIndex)

	// Если поле экспортируемое, возвращаем как есть
	if field.CanSet() {
		return field
	}

	// Для приватных полей используем unsafe
	if structVal.CanAddr() {
		structType := structVal.Type()
		fieldType := structType.Field(fieldIndex)
		fieldPtr := unsafe.Pointer(uintptr(unsafe.Pointer(structVal.UnsafeAddr())) + fieldType.Offset)
		return reflect.NewAt(fieldType.Type, fieldPtr).Elem()
	}

	return field
}

// setBasicValue конвертирует строку rawVal в базовый тип fieldVal.Type()
func setBasicValue(fieldVal reflect.Value, rawVal string) error {
	// Специальный случай: time.Duration
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
	case reflect.Ptr:
		// указатель: если nil – создаём, потом рекурсивно записываем внутрь
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

// setWithReflect записывает cv в fieldVal, поддерживая приватные поля через unsafe
func setWithReflect(fieldVal, cv reflect.Value) error {
	// Пытаемся обычный способ для экспортируемых полей
	if fieldVal.CanSet() {
		fieldVal.Set(cv)
		return nil
	}

	// Для приватных полей используем unsafe, если поле адресуемо
	if fieldVal.CanAddr() {
		ptr := unsafe.Pointer(fieldVal.UnsafeAddr())
		realVal := reflect.NewAt(fieldVal.Type(), ptr).Elem()
		realVal.Set(cv)
		return nil
	}

	return fmt.Errorf("cannot set field of kind %s (not addressable)", fieldVal.Kind())
}

// setMapValue кладёт rawVal (строку) в map[string]X
func setMapValue(mapVal reflect.Value, mapKey, rawVal string) error {
	keyType := mapVal.Type().Key()
	valType := mapVal.Type().Elem()

	// Поддерживаем только string-ключи
	if keyType.Kind() != reflect.String {
		return fmt.Errorf("unsupported map key type %s; only string keys allowed", keyType.Kind())
	}

	// Конвертируем rawVal к типу valType
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

	// Устанавливаем значение в map
	if mapVal.CanSet() {
		mapVal.SetMapIndex(reflect.ValueOf(mapKey), cv)
		return nil
	}

	// Для приватных map полей
	if mapVal.CanAddr() {
		ptr := unsafe.Pointer(mapVal.UnsafeAddr())
		realMap := reflect.NewAt(mapVal.Type(), ptr).Elem()
		realMap.SetMapIndex(reflect.ValueOf(mapKey), cv)
		return nil
	}

	return fmt.Errorf("cannot set map key %q on unexported field", mapKey)
}

// normalize удаляет все '_' и переводит строку к нижнему регистру
func normalize(s string) string {
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "_", "")
}
