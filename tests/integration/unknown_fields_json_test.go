package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

func TestUnknownFieldsJSON_SimpleStruct(t *testing.T) {
	type Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	content := `{
		"host": "localhost",
		"port": 8080,
		"unknown_field": "value"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
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
	if len(fields) != 1 {
		t.Errorf("expected 1 unknown field, got %d: %v", len(fields), fields)
	}

	if fields[0] != "unknown_field" {
		t.Errorf("expected unknown_field, got %s", fields[0])
	}
}

func TestUnknownFieldsJSON_NestedStruct(t *testing.T) {
	type Database struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	type Config struct {
		AppName  string   `json:"app_name"`
		Database Database `json:"database"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	content := `{
		"app_name": "myapp",
		"database": {
			"host": "localhost",
			"port": 5432,
			"password": "secret",
			"extra": {
				"timeout": 30
			}
		},
		"unknown_top": "value"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
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

func TestUnknownFieldsJSON_Arrays(t *testing.T) {
	type Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	type Config struct {
		Servers []Server `json:"servers"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

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

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
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

func TestUnknownFieldsJSON_Maps(t *testing.T) {
	type Config struct {
		Settings map[string]string `json:"settings"`
		Port     int               `json:"port"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	content := `{
		"port": 8080,
		"settings": {
			"key1": "value1",
			"key2": "value2",
			"any_key": "should_be_allowed"
		},
		"unknown": "field"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
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
	// Should only report "unknown", not the map keys
	if len(fields) != 1 {
		t.Errorf("expected 1 unknown field, got %d: %v", len(fields), fields)
	}

	if fields[0] != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", fields[0])
	}
}

func TestUnknownFieldsJSON_DisallowStrict(t *testing.T) {
	type Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	content := `{
		"host": "localhost",
		"port": 8080,
		"unknown": "field"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	_, err = xconfig.Load(cfg,
		xconfig.WithLoader(l),
		xconfig.WithDisallowUnknownFields(),
	)

	if err == nil {
		t.Fatal("expected error when unknown fields are disallowed")
	}

	var unknownErr *loader.UnknownFieldsError
	if _, ok := err.(*loader.UnknownFieldsError); !ok {
		t.Errorf("expected UnknownFieldsError, got: %T - %v", err, err)
	}

	// Check error message contains the unknown field
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected error message to contain 'unknown', got: %s", err.Error())
	}

	// Assign to avoid unused variable error
	_ = unknownErr
}

func TestUnknownFieldsJSON_NoUnknownFields(t *testing.T) {
	type Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	content := `{
		"host": "localhost",
		"port": 8080
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cfg := &Config{}
	os.Args = os.Args[:1]

	c, err := xconfig.Load(cfg,
		xconfig.WithLoader(l),
		xconfig.WithDisallowUnknownFields(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	unknownFields := xconfig.GetUnknownFields(c)
	if len(unknownFields) != 0 {
		t.Errorf("expected no unknown fields, got: %v", unknownFields)
	}

	if cfg.Host != "localhost" {
		t.Errorf("expected Host=localhost, got %s", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected Port=8080, got %d", cfg.Port)
	}
}

func TestUnknownFieldsJSON_CaseInsensitive(t *testing.T) {
	type Config struct {
		AppName string `json:"app_name"`
		Port    int    `json:"port"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	// JSON with different case
	content := `{
		"App_Name": "myapp",
		"PORT": 8080,
		"Unknown": "field"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
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
	// Should only report Unknown, not App_Name or PORT (case-insensitive match)
	if len(fields) != 1 {
		t.Errorf("expected 1 unknown field, got %d: %v", len(fields), fields)
	}
}

func TestUnknownFieldsJSON_ComplexNesting(t *testing.T) {
	type Connection struct {
		Timeout int `json:"timeout"`
	}

	type Database struct {
		Host       string     `json:"host"`
		Port       int        `json:"port"`
		Connection Connection `json:"connection"`
	}

	type Config struct {
		Database Database `json:"database"`
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.json")

	content := `{
		"database": {
			"host": "localhost",
			"port": 5432,
			"connection": {
				"timeout": 30,
				"extra_conn_field": "value"
			},
			"extra_db_field": "value"
		},
		"extra_top_field": "value"
	}`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		".json": json.Unmarshal,
	})
	if err != nil {
		t.Fatalf("failed to create loader: %v", err)
	}

	if err := l.AddFile(testFile, false); err != nil {
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

	// Verify all three levels of unknown fields are detected
	expectedFields := []string{"extra_top_field", "database.extra_db_field", "database.connection.extra_conn_field"}
	for _, expected := range expectedFields {
		found := false
		for _, field := range fields {
			if strings.Contains(field, expected) || strings.EqualFold(field, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find unknown field containing '%s', got: %v", expected, fields)
		}
	}
}
