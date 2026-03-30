package env_test

import (
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/internal/f"
	"github.com/sxwebdev/xconfig/internal/testutil"
	"github.com/sxwebdev/xconfig/plugins/env"
)

const testEnvPrefix = "XCONFIG_TEST"

func TestEnvBasic(t *testing.T) {
	envs := map[string]string{
		"GO_HARD":               "T",
		"VERSION":               "0.2",
		"REDIS_HOST":            "redis-host",
		"REDIS_PORT":            "6379",
		"RETHINK_HOST_ADDRESS":  "rethink-cluster",
		"RETHINK_HOST_PORT":     "28015",
		"RETHINK_DB":            "",
		"BASE_URL_API":          "https://api.example.com",
		"P2P_GROUPS_IS_ENABLED": "true",
		"P2_P_GS_IS_ENABLED":    "true",
	}

	expect := f.Config{
		Anon: f.Anon{
			Version: "0.2",
		},

		GoHard: true,

		Redis: f.Redis{
			Host: "redis-host",
			Port: 6379,
		},

		Rethink: f.RethinkConfig{
			Host: f.Host{
				Address: "rethink-cluster",
				Port:    "28015",
			},
			Db: "",
		},
		BaseURL: f.BaseURLs{
			API: "https://api.example.com",
		},
		P2PGroups: f.P2PGroups{
			IsEnabled: true,
		},
		P2PGs: f.P2PGroups{
			IsEnabled: true,
		},
	}

	for key, value := range envs {
		t.Setenv(key, value)
	}

	value := f.Config{Rethink: f.RethinkConfig{Db: "must-be-override-by-empty-env"}}

	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}

	err = conf.Parse()
	if err != nil {
		t.Fatal(err)
	}

	testutil.Equal(t, expect, value)
}

type fEnv struct {
	Address string `env:"MY_HOST_NAME"`
}

func TestEnvTag(t *testing.T) {
	envs := map[string]string{
		"XCONFIG_TEST_MY_HOST_NAME": "https://blah.bleh",
	}

	for key, value := range envs {
		t.Setenv(key, value)
	}

	expect := fEnv{
		Address: "https://blah.bleh",
	}

	value := fEnv{}

	conf, err := xconfig.Custom(&value, env.New(testEnvPrefix))
	if err != nil {
		t.Fatal(err)
	}

	err = conf.Parse()
	if err != nil {
		t.Fatal(err)
	}

	testutil.Equal(t, expect, value)
}
