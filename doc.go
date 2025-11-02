// Package xconfig provides a lightweight, zero-dependency, and highly extensible
// configuration management library for Go applications.
//
// # Overview
//
// xconfig enables you to build type-safe configuration for your applications using
// a plugin-based architecture. Mix and match only the configuration sources you need:
// defaults, environment variables, command-line flags, configuration files, secret
// providers, and more.
//
// # Quick Start
//
// Define your configuration as a struct with tags:
//
//	type Config struct {
//	    Host     string `default:"localhost" env:"HOST" flag:"host" usage:"Server host"`
//	    Port     int    `default:"8080" env:"PORT" flag:"port" usage:"Server port"`
//	    Debug    bool   `default:"false" env:"DEBUG" flag:"debug" usage:"Debug mode"`
//	    Database struct {
//	        Host     string `default:"localhost" env:"DB_HOST" usage:"Database host"`
//	        Port     int    `default:"5432" env:"DB_PORT" usage:"Database port"`
//	        Password string `secret:"DB_PASSWORD" usage:"Database password"`
//	    }
//	}
//
// Load configuration from multiple sources:
//
//	cfg := &Config{}
//	_, err := xconfig.Load(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// The Load function automatically processes configuration in this order:
//  1. Default values from struct tags
//  2. Custom defaults via SetDefaults() method
//  3. Configuration files (if provided)
//  4. Environment variables
//  5. Command-line flags
//
// # Configuration Sources
//
// ## Default Values
//
// Use the "default" tag to specify default values:
//
//	type Config struct {
//	    Port    int    `default:"8080"`
//	    Timeout int    `default:"30"`
//	    Enabled bool   `default:"true"`
//	}
//
// ## Environment Variables
//
// Use the "env" tag to bind fields to environment variables:
//
//	type Config struct {
//	    APIKey string `env:"API_KEY"`
//	    Secret string `env:"SECRET"`
//	}
//
// Add a prefix to all environment variables:
//
//	_, err := xconfig.Load(cfg, xconfig.WithEnvPrefix("MYAPP"))
//	// Will look for: MYAPP_API_KEY, MYAPP_SECRET
//
// ## Command-Line Flags
//
// Use the "flag" tag to bind fields to command-line flags:
//
//	type Config struct {
//	    Host string `flag:"host" usage:"Server hostname"`
//	    Port int    `flag:"port" usage:"Server port"`
//	}
//
// ## Configuration Files
//
// Load configuration from JSON, YAML, TOML, or any other format:
//
//	import (
//	    "encoding/json"
//	    "github.com/sxwebdev/xconfig/plugins/loader"
//	)
//
//	l, err := loader.NewLoader(map[string]loader.Unmarshal{
//	    ".json": json.Unmarshal,
//	    ".yaml": yaml.Unmarshal,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	err = l.AddFile("config.json", false)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	_, err = xconfig.Load(cfg, xconfig.WithLoader(l))
//
// ## Custom Defaults
//
// Implement SetDefaults() for programmatic default values:
//
//	type Config struct {
//	    URLs []string
//	    Port int
//	}
//
//	func (c *Config) SetDefaults() {
//	    c.URLs = []string{"https://api.example.com"}
//	    c.Port = 8080
//	}
//
// # Secret Management
//
// Use the secret plugin to load sensitive data from secret providers
// like AWS Secrets Manager, HashiCorp Vault, or Kubernetes secrets:
//
//	import "github.com/sxwebdev/xconfig/plugins/secret"
//
//	type Config struct {
//	    DBPassword string `secret:"DATABASE_PASSWORD"`
//	    APIToken   string `secret:"API_TOKEN"`
//	}
//
//	secretProvider := func(name string) (string, error) {
//	    // Fetch from your secret backend
//	    return fetchFromVault(name)
//	}
//
//	_, err := xconfig.Load(cfg, xconfig.WithPlugins(secret.New(secretProvider)))
//
// # Validation
//
// Add validation by implementing the Validate() method:
//
//	type Config struct {
//	    Port int `default:"8080"`
//	}
//
//	func (c *Config) Validate() error {
//	    if c.Port < 1 || c.Port > 65535 {
//	        return fmt.Errorf("invalid port: %d", c.Port)
//	    }
//	    return nil
//	}
//
// Or use external validators with the validate plugin:
//
//	import (
//	    "github.com/go-playground/validator/v10"
//	    "github.com/sxwebdev/xconfig/plugins/validate"
//	)
//
//	type Config struct {
//	    Email string `validate:"required,email"`
//	    Age   int    `validate:"gte=0,lte=130"`
//	}
//
//	v := validator.New()
//	_, err := xconfig.Load(cfg, xconfig.WithPlugins(
//	    validate.New(func(a any) error {
//	        return v.Struct(a)
//	    }),
//	))
//
// # Available Tags
//
// The following struct tags are supported:
//
//   - default: Default value for the field
//   - env: Environment variable name
//   - flag: Command-line flag name
//   - secret: Secret identifier for secret provider
//   - usage: Description for documentation and help text
//   - xconfig: Override field name in flat structure
//
// # Supported Types
//
// xconfig supports all basic Go types, time.Duration, slices of basic types,
// and any type implementing encoding.TextUnmarshaler:
//
//   - string, bool
//   - int, int8, int16, int32, int64
//   - uint, uint8, uint16, uint32, uint64
//   - float32, float64
//   - time.Duration
//   - []string, []int, []float64, etc.
//   - Custom types via encoding.TextUnmarshaler
//
// # Custom Plugins
//
// Create custom plugins by implementing the Plugin interface with either
// Walker or Visitor:
//
//	import (
//	    "github.com/sxwebdev/xconfig/flat"
//	    "github.com/sxwebdev/xconfig/plugins"
//	)
//
//	type myPlugin struct {
//	    fields flat.Fields
//	}
//
//	func (p *myPlugin) Visit(fields flat.Fields) error {
//	    p.fields = fields
//	    return nil
//	}
//
//	func (p *myPlugin) Parse() error {
//	    // Load configuration for each field
//	    return nil
//	}
//
//	// Use your plugin
//	_, err := xconfig.Custom(cfg, &myPlugin{})
//
// # Documentation Generation
//
// Generate markdown documentation for your configuration:
//
//	markdown, err := xconfig.GenerateMarkdown(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	os.WriteFile("CONFIG.md", []byte(markdown), 0644)
//
// # Options
//
// Control which plugins are enabled:
//
//	_, err := xconfig.Load(cfg,
//	    xconfig.WithSkipDefaults(),      // Skip 'default' tags
//	    xconfig.WithSkipEnv(),            // Skip environment variables
//	    xconfig.WithSkipFlags(),          // Skip command-line flags
//	    xconfig.WithEnvPrefix("MYAPP"),   // Add prefix to env vars
//	    xconfig.WithPlugins(myPlugin),    // Add custom plugins
//	)
//
// For more information and examples, see:
// https://github.com/sxwebdev/xconfig
package xconfig
