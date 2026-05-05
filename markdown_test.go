package xconfig_test

import (
	"strings"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/env"
)

// dummyConfig is used for testing.
// The struct tags are assumed to be picked up by xconfig's Load and flat.View.
type dummyConfig struct {
	Foo         string `env:"FOO" required:"" usage:"Foo usage" example:"Foo example"`
	Bar         string `env:"BAR" usage:"Bar usage" example:"Bar example"`
	SecretField string `env:"SECRET_FIELD" secret:"" usage:"Secret usage" example:"Secret example"`
	WithDefault string `env:"WITH_DEFAULT" default:"defaultWithDefault" usage:"WithDefault usage" example:"WithDefault example"`
}

// SetDefaults sets the default values for the dummyConfig.
func (c *dummyConfig) SetDefaults() {
	c.Bar = "defaultBar"
	c.SecretField = "strongSecretPassword"
}

func TestGenerateMarkdown(t *testing.T) {
	cfg := &dummyConfig{}

	output, err := xconfig.GenerateMarkdown(cfg, xconfig.WithSkipFlags())
	if err != nil {
		t.Fatalf("GenerateMarkdown returned error: %v", err)
	}

	// Check for expected environment names wrapped in backticks.
	expectedEnvNames := []string{"`FOO`", "`BAR`", "`SECRET_FIELD`", "`WITH_DEFAULT`"}
	for _, env := range expectedEnvNames {
		if !strings.Contains(output, env) {
			t.Errorf("expected output to contain env name %s, got: %s", env, output)
		}
	}

	// Check for expected usage texts.
	expectedUsages := []string{"Foo usage", "Bar usage", "WithDefault usage"}
	for _, usage := range expectedUsages {
		if !strings.Contains(output, usage) {
			t.Errorf("expected output to contain usage %s, got: %s", usage, output)
		}
	}

	// Check for expected examples (wrapped in code blocks).
	expectedExamples := []string{"`Foo example`", "`Bar example`", "`WithDefault example`"}
	for _, example := range expectedExamples {
		if !strings.Contains(output, example) {
			t.Errorf("expected output to contain example %s, got: %s", example, output)
		}
	}

	// Check for default values.
	expectedDefaults := []string{"defaultWithDefault", "defaultBar"}
	for _, def := range expectedDefaults {
		if !strings.Contains(output, def) {
			t.Errorf("expected output to contain default value %s, got: %s", def, output)
		}
	}

	// Check for secret field.
	if strings.Contains(output, "strongSecretPassword") {
		t.Errorf("expected output to NOT contain secret value, got: %s", output)
	}
}

// TestGenerateMarkdownSliceOfStruct reproduces the case where a Config has a
// pre-populated slice of struct: GenerateMarkdown must produce a row per
// element field with paths like "Nodes.0.GRPCAddr" — without erroring on
// the slice index in the path. It also verifies that an `env:"..."` tag on
// a field inside the slice element acts as a per-segment override (the
// slice index is preserved) — without this, different elements would all
// collide on the same env name.
func TestGenerateMarkdownSliceOfStruct(t *testing.T) {
	type ClusterNodeConfig struct {
		GRPCAddr string `validate:"required" example:"node-1.example.com:443"`
		Headers  string `secret:"true"`
		UseTLS   bool   `env:"USE_TLS"`
	}
	type ClusterConfig struct {
		Nodes []ClusterNodeConfig
	}
	type Config struct {
		Cluster ClusterConfig
	}

	cfg := &Config{
		Cluster: ClusterConfig{
			Nodes: []ClusterNodeConfig{
				{
					GRPCAddr: "node-1.example.com:443",
					UseTLS:   true,
				},
				{
					GRPCAddr: "node-2.example.com:443",
				},
			},
		},
	}

	output, err := xconfig.GenerateMarkdown(
		cfg,
		xconfig.WithEnvPrefix("MYAPP"),
		xconfig.WithSkipFlags(),
		xconfig.WithPlugins(env.New("MYAPP")),
	)
	if err != nil {
		t.Fatalf("GenerateMarkdown returned error: %v", err)
	}

	// Env names for both slice elements must include the prefix and the index.
	// The `env:"USE_TLS"` tag must NOT collapse to "MYAPP_USE_TLS" — that
	// would make Nodes[0].UseTLS and Nodes[1].UseTLS share the same env var.
	expected := []string{
		"`MYAPP_CLUSTER_NODES_0_GRPC_ADDR`",
		"`MYAPP_CLUSTER_NODES_0_HEADERS`",
		"`MYAPP_CLUSTER_NODES_0_USE_TLS`",
		"`MYAPP_CLUSTER_NODES_1_GRPC_ADDR`",
		"`MYAPP_CLUSTER_NODES_1_USE_TLS`",
		"node-1.example.com:443",
		"node-2.example.com:443",
	}
	for _, want := range expected {
		if !strings.Contains(output, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, output)
		}
	}

	// The collapsed-name bug used to produce just "MYAPP_USE_TLS" — make
	// sure that exact (wrong) value isn't anywhere in the output.
	if strings.Contains(output, "`MYAPP_USE_TLS`") {
		t.Errorf("env:\"USE_TLS\" inside slice element must not collapse to MYAPP_USE_TLS; got:\n%s", output)
	}

	// Header (secret:"true") must NOT have its value rendered as default.
	// Since it's empty in the fixture, the only way to be sure is to check
	// the row marks it secret — the icon column should contain ✅ on that row.
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "MYAPP_CLUSTER_NODES_0_HEADERS") {
			if !strings.Contains(line, "✅") {
				t.Errorf("HEADERS row should be marked secret, got: %s", line)
			}
		}
	}
}
