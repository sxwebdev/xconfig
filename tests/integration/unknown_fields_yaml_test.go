package integration_test

import (
	"os"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/decoders/xconfigyaml"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

func TestYAMLUnknownFields(t *testing.T) {
	type LogConfig struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	}

	type ServerConfig struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	}

	type DatabaseConfig struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		Name     string `yaml:"name"`
		Password string `yaml:"password"`
	}

	type Config struct {
		Log      LogConfig      `yaml:"log"`
		Server   ServerConfig   `yaml:"server"`
		Database DatabaseConfig `yaml:"database"`
		DataDir  string         `yaml:"data_dir"`
		Settings map[string]any `yaml:"settings"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_unknown.yaml"
	content := `log:
  level: debug
  format: json
  unknown_log_field: true

server:
  host: localhost
  port: 8080

database:
  host: localhost
  port: 5432
  name: mydb
  password: secret

data_dir: /data
unknown_root_field: value
settings:
  key1: value1
  key2: value2
  headers:
    Authorization: Bearer token
    Content-Type: application/json
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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

	fields := unknownFields[testFile]
	if len(fields) != 2 {
		t.Errorf("expected 2 unknown fields, got %d: %v", len(fields), fields)
	}

	// Verify the specific unknown fields
	hasRootField := false
	hasLogField := false
	for _, field := range fields {
		if field == "unknown_root_field" {
			hasRootField = true
		}
		if field == "log.unknown_log_field" {
			hasLogField = true
		}
	}

	if !hasRootField {
		t.Error("expected to find 'unknown_root_field' in unknown fields")
	}
	if !hasLogField {
		t.Error("expected to find 'log.unknown_log_field' in unknown fields")
	}

	// Verify that known fields were loaded correctly
	if cfg.Log.Level != "debug" {
		t.Errorf("expected log.level=debug, got %s", cfg.Log.Level)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected server.port=8080, got %d", cfg.Server.Port)
	}
}

func TestYAMLDisallowUnknownFields(t *testing.T) {
	type Config struct {
		Version string `yaml:"version"`
		Debug   bool   `yaml:"debug"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_strict.yaml"
	content := `version: "1.0"
debug: true
extra_field: should_fail
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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

	if !isUnknownFieldsError(err) {
		t.Errorf("expected UnknownFieldsError, got: %T - %v", err, err)
	}

	// Check error message
	errMsg := err.Error()
	if !contains(errMsg, "extra_field") {
		t.Errorf("expected error message to contain 'extra_field', got: %s", errMsg)
	}
}

func TestYAMLArraysWithUnknownFields(t *testing.T) {
	type Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	}

	type Config struct {
		Servers []Server `yaml:"servers"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_arrays.yaml"
	content := `servers:
  - host: server1
    port: 8080
    extra: field1
  - host: server2
    port: 8081
  - host: server3
    port: 8082
    another_extra: field2
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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
	if len(fields) != 2 {
		t.Errorf("expected 2 unknown fields, got %d: %v", len(fields), fields)
	}

	// Verify the array was loaded correctly
	if len(cfg.Servers) != 3 {
		t.Errorf("expected 3 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Host != "server1" {
		t.Errorf("expected servers[0].host=server1, got %s", cfg.Servers[0].Host)
	}
}

func TestYAMLMapsWithUnknownFields(t *testing.T) {
	type Config struct {
		Settings map[string]string `yaml:"settings"`
		Version  string            `yaml:"version"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_maps.yaml"
	content := `version: "1.0"
settings:
  key1: value1
  key2: value2
  key3: value3
unknown_map_field: invalid
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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
	if len(fields) != 1 {
		t.Errorf("expected 1 unknown field, got %d: %v", len(fields), fields)
	}

	if fields[0] != "unknown_map_field" {
		t.Errorf("expected unknown field 'unknown_map_field', got %s", fields[0])
	}

	// Verify map was loaded correctly (maps allow any keys)
	if len(cfg.Settings) != 3 {
		t.Errorf("expected 3 settings, got %d", len(cfg.Settings))
	}
	if cfg.Settings["key1"] != "value1" {
		t.Errorf("expected settings[key1]=value1, got %s", cfg.Settings["key1"])
	}
}

func TestYAMLNestedStructuresWithUnknownFields(t *testing.T) {
	type Credentials struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	}

	type DatabaseConfig struct {
		Host        string      `yaml:"host"`
		Port        int         `yaml:"port"`
		Credentials Credentials `yaml:"credentials"`
	}

	type Config struct {
		Database DatabaseConfig `yaml:"database"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_nested.yaml"
	content := `database:
  host: localhost
  port: 5432
  credentials:
    username: admin
    password: secret
    token: invalid_field
  extra_db_field: invalid
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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
	if len(fields) != 2 {
		t.Errorf("expected 2 unknown fields, got %d: %v", len(fields), fields)
	}

	// Check for deeply nested unknown field
	hasCredToken := false
	hasDbExtra := false
	for _, field := range fields {
		if field == "database.credentials.token" {
			hasCredToken = true
		}
		if field == "database.extra_db_field" {
			hasDbExtra = true
		}
	}

	if !hasCredToken {
		t.Error("expected to find 'database.credentials.token' in unknown fields")
	}
	if !hasDbExtra {
		t.Error("expected to find 'database.extra_db_field' in unknown fields")
	}

	// Verify nested structure was loaded correctly
	if cfg.Database.Credentials.Username != "admin" {
		t.Errorf("expected database.credentials.username=admin, got %s", cfg.Database.Credentials.Username)
	}
}

func TestYAMLCaseInsensitiveFields(t *testing.T) {
	type LogConfig struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	}

	type Config struct {
		Log     LogConfig `yaml:"log"`
		DataDir string    `yaml:"data_dir"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_case.yaml"
	content := `log:
  level: debug
  format: json
data_dir: /data
UNKNOWN_FIELD: invalid
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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
	if len(fields) != 1 {
		t.Errorf("expected 1 unknown field, got %d: %v", len(fields), fields)
	}

	if fields[0] != "UNKNOWN_FIELD" {
		t.Errorf("expected unknown field 'UNKNOWN_FIELD', got %s", fields[0])
	}

	// Verify case-insensitive matching worked for valid fields
	if cfg.Log.Level != "debug" {
		t.Errorf("expected log.level=debug, got %s", cfg.Log.Level)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("expected data_dir=/data, got %s", cfg.DataDir)
	}
}

func TestYAMLArrayOfMapsWithUnknownFields(t *testing.T) {
	type Feature struct {
		Name    string            `yaml:"name"`
		Enabled bool              `yaml:"enabled"`
		Config  map[string]string `yaml:"config"`
	}

	type Config struct {
		Features []Feature `yaml:"features"`
	}

	tmpDir := t.TempDir()
	testFile := tmpDir + "/config_array_maps.yaml"
	content := `features:
  - name: feature1
    enabled: true
    config:
      key1: value1
      key2: value2
    invalid_feature_field: bad
  - name: feature2
    enabled: false
    config:
      key3: value3
`

	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
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
	if len(fields) != 1 {
		t.Errorf("expected 1 unknown field, got %d: %v", len(fields), fields)
	}

	// Verify structures were loaded correctly
	if len(cfg.Features) != 2 {
		t.Errorf("expected 2 features, got %d", len(cfg.Features))
	}
	if cfg.Features[0].Name != "feature1" {
		t.Errorf("expected features[0].name=feature1, got %s", cfg.Features[0].Name)
	}
	if len(cfg.Features[0].Config) != 2 {
		t.Errorf("expected 2 config items, got %d", len(cfg.Features[0].Config))
	}
}

// Helper functions
func isUnknownFieldsError(err error) bool {
	_, ok := err.(*loader.UnknownFieldsError)
	return ok
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
