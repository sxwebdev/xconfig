package integration_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/loader"
)

type BlockchainType string

const (
	Ethereum BlockchainType = "ethereum"
)

type ChainConfig struct {
	Blockchain       BlockchainType `json:"blockchain" yaml:"blockchain" validate:"required"`
	ChainID          int64          `json:"chain_id" yaml:"chain_id" validate:"required"`
	MinConfirmations int            `json:"min_confirmations" yaml:"min_confirmations" default:"12"`
	BlockSyncOffset  int64          `json:"block_sync_offset" yaml:"block_sync_offset" default:"100"`
}

type ParserConfig struct {
	Enabled       bool          `json:"enabled" yaml:"enabled" default:"true"`
	FirstBlock    int64         `json:"first_block" yaml:"first_block" default:"-1"`
	BlocksInChunk int           `json:"blocks_in_chunk" yaml:"blocks_in_chunk" default:"5"`
	Workers       int           `json:"workers" yaml:"workers" default:"1"`
	Timeout       time.Duration `json:"timeout" yaml:"timeout" default:"30s"`
}

type IndexerConfig struct {
	Chain  ChainConfig  `json:"chain" yaml:"chain"`
	Parser ParserConfig `json:"parser" yaml:"parser"`
}

type TestConfig struct {
	Indexers map[BlockchainType]IndexerConfig `json:"indexers" yaml:"indexers"`
}

func TestMapDefaultsWithJSON(t *testing.T) {
	jsonContent := `{
  "indexers": {
    "ethereum": {
      "chain": {
        "blockchain": "ethereum",
        "chain_id": 1
      },
      "parser": {
        "enabled": true,
        "first_block": 100
      }
    }
  }
}`

	l, err := loader.NewLoader(map[string]loader.Unmarshal{
		"json": json.Unmarshal,
	})
	if err != nil {
		t.Fatal(err)
	}

	tmpfile := t.TempDir() + "/config.json"
	if err := os.WriteFile(tmpfile, []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := l.AddFile(tmpfile, false); err != nil {
		t.Fatal(err)
	}

	cfg := &TestConfig{}

	_, err = xconfig.Load(cfg,
		xconfig.WithDisallowUnknownFields(),
		xconfig.WithLoader(l),
		xconfig.WithSkipFlags(),
	)
	if err != nil {
		t.Fatal(err)
	}

	expect := TestConfig{
		Indexers: map[BlockchainType]IndexerConfig{
			Ethereum: {
				Chain: ChainConfig{
					Blockchain:       Ethereum,
					ChainID:          1,
					MinConfirmations: 12,
					BlockSyncOffset:  100,
				},
				Parser: ParserConfig{
					Enabled:       true,
					FirstBlock:    100,
					BlocksInChunk: 5,
					Workers:       1,
					Timeout:       30 * time.Second,
				},
			},
		},
	}

	if diff := cmp.Diff(expect, *cfg); diff != "" {
		t.Errorf("Config mismatch (-want +got):\n%s", diff)
	}
}
