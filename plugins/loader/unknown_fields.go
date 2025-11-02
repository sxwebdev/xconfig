package loader

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// UnknownFieldsError represents an error when unknown fields are found in configuration files.
type UnknownFieldsError struct {
	// Fields contains a map of file paths to their unknown fields
	Fields map[string][]string
}

// Error implements the error interface.
func (e *UnknownFieldsError) Error() string {
	if len(e.Fields) == 0 {
		return "unknown fields found in configuration"
	}

	var parts []string
	for file, fields := range e.Fields {
		sort.Strings(fields)
		parts = append(parts, fmt.Sprintf("%s: %s", file, strings.Join(fields, ", ")))
	}
	sort.Strings(parts)

	return fmt.Sprintf("unknown fields found in configuration files: %s", strings.Join(parts, "; "))
}

// UnknownField represents a single unknown field with its path and source file.
type UnknownField struct {
	Path string // Field path (e.g., "Database.Extra.Field")
	File string // Source file path
}

// findUnknownFields compares the raw data with the struct and returns unknown fields.
// It uses the provided unmarshal function to parse the data into a generic map.
func findUnknownFields(data []byte, v any, filepath string, unmarshal Unmarshal) ([]string, error) {
	var raw map[string]any

	// Try to unmarshal into a generic map using the provided unmarshal function
	err := unmarshal(data, &raw)
	if err != nil {
		// Also try JSON as fallback
		err = json.Unmarshal(data, &raw)
		if err != nil {
			// If both fail, we can't validate - return no error to allow other formats
			return nil, nil
		}
	}

	// Get valid field names from struct
	validFields := getValidFields(reflect.TypeOf(v))

	// Find unknown fields
	unknown := compareFields("", raw, validFields)

	return unknown, nil
}

// getValidFields extracts all valid field names from a struct type.
func getValidFields(t reflect.Type) map[string]bool {
	if t == nil {
		return nil
	}

	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return nil
	}

	fields := make(map[string]bool)
	collectStructFields(t, fields)

	return fields
}

// collectStructFields recursively collects all valid field paths from a struct.
func collectStructFields(t reflect.Type, fields map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get field name from tags (try yaml, json, then use struct field name)
		fieldName := ""

		// Try YAML tag first (most common in your case)
		if yamlTag := field.Tag.Get("yaml"); yamlTag != "" {
			parts := strings.Split(yamlTag, ",")
			if parts[0] == "-" {
				// Skip this field if tagged with "-"
				continue
			}
			if parts[0] != "" {
				fieldName = parts[0]
			}
		}

		// Try JSON tag if yaml tag not found
		if fieldName == "" {
			if jsonTag := field.Tag.Get("json"); jsonTag != "" {
				parts := strings.Split(jsonTag, ",")
				if parts[0] == "-" {
					// Skip this field if tagged with "-"
					continue
				}
				if parts[0] != "" {
					fieldName = parts[0]
				}
			}
		}

		// If no tag found, use field name
		if fieldName == "" {
			fieldName = field.Name
		}

		// Get field type and dereference if pointer
		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		// Handle anonymous/embedded structs
		if field.Anonymous && fieldType.Kind() == reflect.Struct {
			// For anonymous structs, add their fields directly without prefix
			collectStructFields(fieldType, fields)
			continue
		}

		fields[fieldName] = true

		// Handle nested structs
		if fieldType.Kind() == reflect.Struct {
			nestedFields := make(map[string]bool)
			collectStructFields(fieldType, nestedFields)

			// Add nested field paths
			for nestedField := range nestedFields {
				fields[fieldName+"."+nestedField] = true
			}
		}

		// Handle slices of structs
		if fieldType.Kind() == reflect.Slice {
			elemType := fieldType.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
			if elemType.Kind() == reflect.Struct {
				nestedFields := make(map[string]bool)
				collectStructFields(elemType, nestedFields)

				// Add nested field paths for array elements
				for nestedField := range nestedFields {
					fields[fieldName+"[]."+nestedField] = true
				}
			}
		}

		// Handle maps
		if fieldType.Kind() == reflect.Map {
			// Maps are dynamic, so we mark the field as valid
			// and allow any subfields
			fields[fieldName+".*"] = true
		}
	}
}

// compareFields recursively compares raw data with valid fields and returns unknown field paths.
func compareFields(prefix string, data any, validFields map[string]bool) []string {
	var unknown []string

	switch v := data.(type) {
	case map[string]any:
		for key, value := range v {
			fieldPath := key
			if prefix != "" {
				fieldPath = prefix + "." + key
			}

			// Check if this field is valid (try both exact match and case-insensitive)
			isValid := validFields[fieldPath]

			// Try case-insensitive match
			if !isValid {
				isValid = isFieldValid(fieldPath, validFields)
			}

			// Check if parent allows dynamic fields (map)
			if !isValid && prefix != "" {
				isValid = validFields[prefix+".*"]
			}

			// Check if this is a valid top-level field (case-insensitive)
			if !isValid && prefix == "" {
				isValid = validFields[key]
				if !isValid {
					isValid = isFieldValid(key, validFields)
				}
			}

			if !isValid {
				unknown = append(unknown, fieldPath)
			} else {
				// Recursively check nested objects
				if nested, ok := value.(map[string]any); ok {
					// Find the actual field path for nested objects
					actualFieldPath := findActualFieldPath(fieldPath, validFields)
					nestedUnknown := compareFields(actualFieldPath, nested, validFields)
					unknown = append(unknown, nestedUnknown...)
				}

				// Check arrays
				if arr, ok := value.([]any); ok {
					for _, item := range arr {
						if nestedMap, ok := item.(map[string]any); ok {
							// Check against array element pattern
							actualFieldPath := findActualFieldPath(fieldPath, validFields)
							arrayPattern := actualFieldPath + "[]"
							nestedUnknown := compareFields(arrayPattern, nestedMap, validFields)
							unknown = append(unknown, nestedUnknown...)
						}
					}
				}
			}
		}

	case []any:
		// Handle arrays at root level
		for _, item := range v {
			if nestedMap, ok := item.(map[string]any); ok {
				nestedUnknown := compareFields(prefix, nestedMap, validFields)
				unknown = append(unknown, nestedUnknown...)
			}
		}
	}

	return unknown
}

// isFieldValid checks if a field is valid using case-insensitive comparison
func isFieldValid(fieldPath string, validFields map[string]bool) bool {
	lowerFieldPath := strings.ToLower(fieldPath)
	for validField := range validFields {
		if strings.ToLower(validField) == lowerFieldPath {
			return true
		}
	}
	return false
}

// findActualFieldPath finds the actual field path from validFields map (case-insensitive)
func findActualFieldPath(fieldPath string, validFields map[string]bool) string {
	// First try exact match
	if validFields[fieldPath] {
		return fieldPath
	}

	// Try case-insensitive match
	lowerFieldPath := strings.ToLower(fieldPath)
	for validField := range validFields {
		if strings.ToLower(validField) == lowerFieldPath {
			return validField
		}
	}

	return fieldPath
}
