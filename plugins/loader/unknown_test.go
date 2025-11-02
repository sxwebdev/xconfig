package loader_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

func TestUnknownFieldsDetection(t *testing.T) {
	type Config struct {
		Version string `json:"Version"`
		Redis   struct {
			Host string `json:"Host"`
			Port int    `json:"Port"`
		} `json:"Redis"`
	}

	// Create a test file with unknown fields
	testFile := "testdata/unknown_fields.json"
	content := `{
		"Version": "1.0",
		"Redis": {
			"Host": "localhost",
			"Port": 6379,
			"Unknown": "value"
		},
		"ExtraField": "should not be here"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	err = l.AddFile(testFile, false)
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	// Load without disallow - should succeed
	c, err := xconfig.Load(cfg, xconfig.WithLoader(l))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	// Check that unknown fields were detected
	unknownFields := xconfig.GetUnknownFields(c)
	if len(unknownFields) == 0 {
		t.Error("expected unknown fields to be detected")
	}

	if fields, ok := unknownFields[testFile]; ok {
		if len(fields) != 2 {
			t.Errorf("expected 2 unknown fields, got %d: %v", len(fields), fields)
		}

		// Check for specific fields
		hasExtraField := false
		hasUnknown := false
		for _, field := range fields {
			if field == "ExtraField" {
				hasExtraField = true
			}
			if field == "Redis.Unknown" {
				hasUnknown = true
			}
		}

		if !hasExtraField {
			t.Error("expected to find 'ExtraField' in unknown fields")
		}
		if !hasUnknown {
			t.Error("expected to find 'Redis.Unknown' in unknown fields")
		}
	} else {
		t.Errorf("expected unknown fields for file %s", testFile)
	}
}

func TestDisallowUnknownFields(t *testing.T) {
	type Config struct {
		Version string `json:"Version"`
		Redis   struct {
			Host string `json:"Host"`
			Port int    `json:"Port"`
		} `json:"Redis"`
	}

	// Create a test file with unknown fields
	testFile := "testdata/unknown_fields_strict.json"
	content := `{
		"Version": "1.0",
		"Redis": {
			"Host": "localhost",
			"Port": 6379,
			"Password": "secret"
		}
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	err = l.AddFile(testFile, false)
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	// Load with disallow - should fail
	_, err = xconfig.Load(cfg,
		xconfig.WithLoader(l),
		xconfig.WithDisallowUnknownFields(),
	)

	if err == nil {
		t.Fatal("expected error when unknown fields are disallowed")
	}

	// Check that it's the correct error type
	var unknownErr *loader.UnknownFieldsError
	if !errors.As(err, &unknownErr) {
		t.Errorf("expected UnknownFieldsError, got: %T - %v", err, err)
	}
}

func TestNoUnknownFields(t *testing.T) {
	type Config struct {
		Version string `json:"Version"`
		Redis   struct {
			Host string `json:"Host"`
			Port int    `json:"Port"`
		} `json:"Redis"`
	}

	// Create a test file without unknown fields
	testFile := "testdata/no_unknown_fields.json"
	content := `{
		"Version": "1.0",
		"Redis": {
			"Host": "localhost",
			"Port": 6379
		}
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	err = l.AddFile(testFile, false)
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	// Load with disallow - should succeed
	c, err := xconfig.Load(cfg,
		xconfig.WithLoader(l),
		xconfig.WithDisallowUnknownFields(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that no unknown fields were detected
	unknownFields := xconfig.GetUnknownFields(c)
	if len(unknownFields) != 0 {
		t.Errorf("expected no unknown fields, got: %v", unknownFields)
	}

	// Verify config was loaded correctly
	if cfg.Version != "1.0" {
		t.Errorf("expected Version=1.0, got %s", cfg.Version)
	}
	if cfg.Redis.Host != "localhost" {
		t.Errorf("expected Redis.Host=localhost, got %s", cfg.Redis.Host)
	}
	if cfg.Redis.Port != 6379 {
		t.Errorf("expected Redis.Port=6379, got %d", cfg.Redis.Port)
	}
}

func TestNestedUnknownFields(t *testing.T) {
	type Database struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	type Config struct {
		Database Database `json:"database"`
	}

	testFile := "testdata/nested_unknown.json"
	content := `{
		"database": {
			"host": "localhost",
			"port": 5432,
			"password": "secret",
			"connection": {
				"timeout": 30
			}
		},
		"extra": "value"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	err = l.AddFile(testFile, false)
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	c, err := xconfig.Load(cfg, xconfig.WithLoader(l))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	unknownFields := xconfig.GetUnknownFields(c)
	if len(unknownFields) == 0 {
		t.Fatal("expected unknown fields to be detected")
	}

	fields := unknownFields[testFile]
	if len(fields) != 3 {
		t.Errorf("expected 3 unknown fields, got %d: %v", len(fields), fields)
	}
}

func TestUnknownFieldsError(t *testing.T) {
	err := &loader.UnknownFieldsError{
		Fields: map[string][]string{
			"config.json": {"field1", "field2"},
			"app.json":    {"field3"},
		},
	}

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("expected non-empty error message")
	}

	// Check that error message contains file names and fields
	if !contains(errMsg, "config.json") {
		t.Error("expected error message to contain 'config.json'")
	}
	if !contains(errMsg, "field1") {
		t.Error("expected error message to contain 'field1'")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestArraysWithUnknownFields(t *testing.T) {
	type Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	type Config struct {
		Servers []Server `json:"servers"`
	}

	testFile := "testdata/arrays_unknown.json"
	content := `{
		"servers": [
			{
				"host": "server1",
				"port": 8080,
				"extra": "field"
			},
			{
				"host": "server2",
				"port": 8081
			}
		]
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	err = l.AddFile(testFile, false)
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	c, err := xconfig.Load(cfg, xconfig.WithLoader(l))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	unknownFields := xconfig.GetUnknownFields(c)
	if len(unknownFields) == 0 {
		t.Fatal("expected unknown fields to be detected")
	}

	fields := unknownFields[testFile]
	if len(fields) == 0 {
		t.Error("expected to find unknown fields in array elements")
	}
}
