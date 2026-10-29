//go:build ignore

package main

import (
	"os"
	"strings"
	"testing"
)

func TestWorkspaceUsesEveryModule(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := workspace(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("go.work")
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if !strings.Contains(string(data), "\t./"+module+"\n") {
			t.Fatalf("workspace does not use %s:\n%s", module, data)
		}
	}
	if strings.Contains(string(data), "replace") {
		t.Fatalf("workspace replaces modules instead of using them:\n%s", data)
	}
}
