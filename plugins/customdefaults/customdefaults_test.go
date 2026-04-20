package customdefaults_test

import (
	"testing"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/internal/testutil"
	"github.com/sxwebdev/xconfig/plugins/customdefaults"
)

type fDefaults struct {
	Field string
}

func (f *fDefaults) SetDefaults() {
	f.Field = "default"
}

func TestCustomDefaultTag(t *testing.T) {
	in := fDefaults{}

	conf, err := xconfig.Custom(&in, customdefaults.New())
	if err != nil {
		t.Fatal(err)
	}

	err = conf.Parse()
	if err != nil {
		t.Fatal(err)
	}

	if in.Field != "default" {
		t.Errorf("expected default but got %v", in.Field)
	}
}

type childItem struct {
	Name   string
	Marked bool
}

func (c *childItem) SetDefaults() {
	if c.Name == "" {
		c.Name = "child"
	}
	c.Marked = true
}

type rootWithChildren struct {
	Single     childItem
	Items      []childItem
	PtrItems   []*childItem
	ByName     map[string]childItem
	ByNamePtr  map[string]*childItem
	RootCalled bool
}

func (r *rootWithChildren) SetDefaults() {
	r.RootCalled = true
}

func TestCustomDefaultsRecursesIntoNestedStructs(t *testing.T) {
	in := rootWithChildren{
		Items:     []childItem{{}, {Name: "explicit"}},
		PtrItems:  []*childItem{{Name: "ptr"}, nil},
		ByName:    map[string]childItem{"a": {}, "b": {Name: "b-explicit"}},
		ByNamePtr: map[string]*childItem{"p": {}},
	}

	conf, err := xconfig.Custom(&in, customdefaults.New())
	if err != nil {
		t.Fatal(err)
	}

	if err := conf.Parse(); err != nil {
		t.Fatal(err)
	}

	expect := rootWithChildren{
		Single: childItem{Name: "child", Marked: true},
		Items: []childItem{
			{Name: "child", Marked: true},
			{Name: "explicit", Marked: true},
		},
		PtrItems: []*childItem{
			{Name: "ptr", Marked: true},
			nil,
		},
		ByName: map[string]childItem{
			"a": {Name: "child", Marked: true},
			"b": {Name: "b-explicit", Marked: true},
		},
		ByNamePtr: map[string]*childItem{
			"p": {Name: "child", Marked: true},
		},
		RootCalled: true,
	}

	testutil.Equal(t, expect, in)
}
