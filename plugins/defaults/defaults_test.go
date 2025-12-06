package defaults_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/defaults"
)

type mapKeyType string

const (
	mapKey1 mapKeyType = "key1"
)

type mapValue struct {
	Name    string `default:"name-default"`
	Value   int    `default:"42"`
	BoolVal bool   `default:"true"`
}

type fDefaults struct {
	Address string        `default:"https://blah.bleh"`
	Bases   []string      `default:"list,blah"`
	Timeout time.Duration `default:"5s"`
	Ignored string
	Map     map[mapKeyType]mapValue
}

func TestDefaultTag(t *testing.T) {
	expect := fDefaults{
		Address: "https://blah.bleh",
		Bases:   []string{"list", "blah"},
		Timeout: 5 * time.Second,
		Ignored: "not-empty",
		Map: map[mapKeyType]mapValue{
			mapKey1: {
				Name:    "name-default",
				Value:   42,
				BoolVal: true,
			},
		},
	}

	value := fDefaults{
		Ignored: "not-empty",
		Map: map[mapKeyType]mapValue{
			mapKey1: {},
		},
	}

	conf, err := xconfig.Custom(&value, defaults.New())
	if err != nil {
		t.Fatal(err)
	}

	err = conf.Parse()
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(expect, value); diff != "" {
		t.Error(diff)
	}
}
