package integration_test

import (
	"os"
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/decoders/xconfigyaml"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

type presenceChain struct {
	Blockchain string `yaml:"blockchain"`
	ChainID    int64  `yaml:"chain_id"`
}

type presenceParser struct {
	Enabled bool `json:"enabled" yaml:"enabled" default:"true"`
}

type presenceIndexer struct {
	// Intentionally NO yaml tag here; config uses the lower-cased key "parser".
	Parser presenceParser
	// Also intentionally no yaml tag here; config uses "chain".
	Chain presenceChain
}

type presenceRoot struct {
	Indexers map[string]presenceIndexer `json:"indexers" yaml:"indexers"`
}

func TestExplicitFalseNotOverriddenWhenParentFieldUntagged_Yaml(t *testing.T) {
	yamlContent := `indexers:
  arbitrum:
    chain:
      blockchain: arbitrum
      chain_id: 42161
    parser:
      enabled: false
`

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"yaml": xconfigyaml.New().Unmarshal,
	})
	if err != nil {
		t.Fatal(err)
	}

	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.AddFile(path, false); err != nil {
		t.Fatal(err)
	}

	cfg := &presenceRoot{}
	_, err = xconfig.Load(cfg,
		xconfig.WithLoader(l),
		xconfig.WithSkipEnv(),
		xconfig.WithSkipFlags(),
	)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.Indexers["arbitrum"].Parser.Enabled
	if got != false {
		t.Fatalf("expected parser.enabled=false from file, got %v", got)
	}
}
