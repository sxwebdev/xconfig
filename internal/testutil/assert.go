package testutil

import (
	"reflect"
	"testing"
)

func Equal(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("mismatch:\nwant: %+v\ngot:  %+v", want, got)
	}
}
