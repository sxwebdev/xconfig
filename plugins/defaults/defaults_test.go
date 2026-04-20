package defaults_test

import (
	"testing"
	"time"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/internal/testutil"
	"github.com/sxwebdev/xconfig/plugins/defaults"
)

type mapKeyType string

const (
	mapKey1 mapKeyType = "key1"
)

type nestedStruct struct {
	InnerField string `default:"inner-default"`
	BoolVal    bool   `default:"true"`
	IntVal     int    `default:"7"`
}

type mapValue struct {
	Name    string `default:"name-default"`
	Value   int    `default:"42"`
	BoolVal bool   `default:"true"`
	Nested  nestedStruct
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
				Nested: nestedStruct{
					InnerField: "inner-default",
					BoolVal:    true,
					IntVal:     7,
				},
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

	testutil.Equal(t, expect, value)
}

type sliceItem struct {
	Enabled bool   `default:"true"`
	Name    string `default:"anon"`
	Port    int    `default:"8080"`
}

type fSliceDefaults struct {
	Items    []sliceItem
	PtrItems []*sliceItem
}

func TestDefaultTagInSliceOfStructs(t *testing.T) {
	value := fSliceDefaults{
		Items: []sliceItem{
			{Name: "first"},
			{Port: 9090},
			{Enabled: true, Name: "explicit", Port: 1234},
		},
		PtrItems: []*sliceItem{
			{Name: "ptr-first"},
			nil,
		},
	}

	conf, err := xconfig.Custom(&value, defaults.New())
	if err != nil {
		t.Fatal(err)
	}

	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := fSliceDefaults{
		Items: []sliceItem{
			{Enabled: true, Name: "first", Port: 8080},
			{Enabled: true, Name: "anon", Port: 9090},
			{Enabled: true, Name: "explicit", Port: 1234},
		},
		PtrItems: []*sliceItem{
			{Enabled: true, Name: "ptr-first", Port: 8080},
			nil,
		},
	}

	testutil.Equal(t, expect, value)
}
