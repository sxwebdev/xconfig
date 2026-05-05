package env_test

import (
	"testing"
	"time"

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

type item struct {
	Key1 string
	Key2 string
}

type sliceConfig struct {
	Items []item
}

func TestEnvSliceOfStruct(t *testing.T) {
	t.Setenv("ITEMS_0_KEY_1", "a")
	t.Setenv("ITEMS_0_KEY_2", "b")
	t.Setenv("ITEMS_1_KEY_1", "c")

	value := sliceConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := sliceConfig{
		Items: []item{
			{Key1: "a", Key2: "b"},
			{Key1: "c", Key2: ""},
		},
	}
	testutil.Equal(t, expect, value)
}

func TestEnvSliceOfStructPrePopulated(t *testing.T) {
	// Slice already has 2 entries; env-vars override the second and add a third.
	t.Setenv("ITEMS_1_KEY_1", "overridden")
	t.Setenv("ITEMS_2_KEY_1", "new-third")

	value := sliceConfig{
		Items: []item{
			{Key1: "first", Key2: "first2"},
			{Key1: "second", Key2: "second2"},
		},
	}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := sliceConfig{
		Items: []item{
			{Key1: "first", Key2: "first2"},
			{Key1: "overridden", Key2: "second2"},
			{Key1: "new-third", Key2: ""},
		},
	}
	testutil.Equal(t, expect, value)
}

type ptrSliceConfig struct {
	Items []*item
}

func TestEnvSliceOfPointerStruct(t *testing.T) {
	t.Setenv("ITEMS_0_KEY_1", "p1")
	t.Setenv("ITEMS_1_KEY_2", "p2b")

	value := ptrSliceConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := ptrSliceConfig{
		Items: []*item{
			{Key1: "p1"},
			{Key2: "p2b"},
		},
	}
	testutil.Equal(t, expect, value)
}

func TestEnvSliceOfStructWithPrefix(t *testing.T) {
	t.Setenv("MYAPP_ITEMS_0_KEY_1", "a")
	t.Setenv("MYAPP_ITEMS_0_KEY_2", "b")

	value := sliceConfig{}
	conf, err := xconfig.Custom(&value, env.New("MYAPP"))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := sliceConfig{
		Items: []item{{Key1: "a", Key2: "b"}},
	}
	testutil.Equal(t, expect, value)
}

type taggedSliceConfig struct {
	Things []item `env:"THINGS"`
}

func TestEnvSliceOfStructWithEnvTag(t *testing.T) {
	t.Setenv("THINGS_0_KEY_1", "a")
	t.Setenv("THINGS_1_KEY_1", "c")
	t.Setenv("THINGS_1_KEY_2", "d")

	value := taggedSliceConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := taggedSliceConfig{
		Things: []item{
			{Key1: "a"},
			{Key1: "c", Key2: "d"},
		},
	}
	testutil.Equal(t, expect, value)
}

type primitiveMapConfig struct {
	Tags map[string]string
}

func TestEnvMapPrimitive(t *testing.T) {
	t.Setenv("TAGS_FOO", "v1")
	t.Setenv("TAGS_BAR", "v2")

	value := primitiveMapConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := primitiveMapConfig{
		Tags: map[string]string{"FOO": "v1", "BAR": "v2"},
	}
	testutil.Equal(t, expect, value)
}

type intMapConfig struct {
	Counts    map[string]int
	Durations map[string]time.Duration
}

func TestEnvMapPrimitiveTypes(t *testing.T) {
	t.Setenv("COUNTS_A", "10")
	t.Setenv("COUNTS_B", "42")
	t.Setenv("DURATIONS_FAST", "5s")
	t.Setenv("DURATIONS_SLOW", "1h")

	value := intMapConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := intMapConfig{
		Counts: map[string]int{"A": 10, "B": 42},
		Durations: map[string]time.Duration{
			"FAST": 5 * time.Second,
			"SLOW": time.Hour,
		},
	}
	testutil.Equal(t, expect, value)
}

func TestEnvMapPrimitivePrePopulated(t *testing.T) {
	t.Setenv("TAGS_BAR", "overridden")
	t.Setenv("TAGS_NEW", "fresh")

	value := primitiveMapConfig{
		Tags: map[string]string{
			"FOO": "kept",
			"BAR": "old",
		},
	}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := primitiveMapConfig{
		Tags: map[string]string{
			"FOO": "kept",
			"BAR": "overridden",
			"NEW": "fresh",
		},
	}
	testutil.Equal(t, expect, value)
}

type server struct {
	Host string
	Port int
}

type structMapConfig struct {
	Servers map[string]server
}

func TestEnvMapOfStruct(t *testing.T) {
	t.Setenv("SERVERS_PRIMARY_HOST", "10.0.0.1")
	t.Setenv("SERVERS_PRIMARY_PORT", "5432")
	t.Setenv("SERVERS_BACKUP_HOST", "10.0.0.2")

	value := structMapConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := structMapConfig{
		Servers: map[string]server{
			"PRIMARY": {Host: "10.0.0.1", Port: 5432},
			"BACKUP":  {Host: "10.0.0.2"},
		},
	}
	testutil.Equal(t, expect, value)
}

type ptrStructMapConfig struct {
	Servers map[string]*server
}

func TestEnvMapOfPointerStruct(t *testing.T) {
	t.Setenv("SERVERS_A_HOST", "h-a")
	t.Setenv("SERVERS_A_PORT", "1111")
	t.Setenv("SERVERS_B_HOST", "h-b")

	value := ptrStructMapConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := ptrStructMapConfig{
		Servers: map[string]*server{
			"A": {Host: "h-a", Port: 1111},
			"B": {Host: "h-b"},
		},
	}
	testutil.Equal(t, expect, value)
}

type taggedItem struct {
	Key1 string
	Key2 string
	Key3 int `env:"KEY3"`
}

type sliceWithTaggedFieldConfig struct {
	Items []taggedItem
}

func TestEnvSliceOfStructWithFieldEnvTag(t *testing.T) {
	// `env:"KEY3"` on a field inside a slice element should act as a per-segment
	// override — slice index must still be in the env name so different
	// elements don't collide.
	t.Setenv("ITEMS_0_KEY_1", "a")
	t.Setenv("ITEMS_0_KEY3", "1")
	t.Setenv("ITEMS_1_KEY3", "2")

	value := sliceWithTaggedFieldConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := sliceWithTaggedFieldConfig{
		Items: []taggedItem{
			{Key1: "a", Key3: 1},
			{Key3: 2},
		},
	}
	testutil.Equal(t, expect, value)
}

type taggedServer struct {
	Host string
	Port int `env:"P"`
}

type structMapWithTaggedFieldConfig struct {
	Servers map[string]taggedServer
}

func TestEnvMapOfStructWithFieldEnvTag(t *testing.T) {
	// `env:"P"` on Port → per-entry env name "<MAP>_<KEY>_P".
	// Without the relative-tag rule, all entries would collide on env "P".
	t.Setenv("SERVERS_PRIMARY_HOST", "10.0.0.1")
	t.Setenv("SERVERS_PRIMARY_P", "5432")
	t.Setenv("SERVERS_BACKUP_P", "5433")

	value := structMapWithTaggedFieldConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := structMapWithTaggedFieldConfig{
		Servers: map[string]taggedServer{
			"PRIMARY": {Host: "10.0.0.1", Port: 5432},
			"BACKUP":  {Port: 5433},
		},
	}
	testutil.Equal(t, expect, value)
}

type combinedConfig struct {
	Items []item
	Tags  map[string]string
}

func TestEnvMapAndSliceCombined(t *testing.T) {
	t.Setenv("ITEMS_0_KEY_1", "a")
	t.Setenv("ITEMS_0_KEY_2", "b")
	t.Setenv("TAGS_FOO", "v1")

	value := combinedConfig{}
	conf, err := xconfig.Custom(&value, env.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := combinedConfig{
		Items: []item{{Key1: "a", Key2: "b"}},
		Tags:  map[string]string{"FOO": "v1"},
	}
	testutil.Equal(t, expect, value)
}
