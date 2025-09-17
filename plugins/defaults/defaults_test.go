package defaults_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins/defaults"
)

type fDefaults struct {
	Address string        `default:"https://blah.bleh"`
	Bases   []string      `default:"list,blah"`
	Timeout time.Duration `default:"5s"`
	Ignored string
}

func TestDefaultTag(t *testing.T) {
	expect := fDefaults{
		Address: "https://blah.bleh",
		Bases:   []string{"list", "blah"},
		Timeout: 5 * time.Second,
		Ignored: "not-empty",
	}

	value := fDefaults{Ignored: "not-empty"}

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
