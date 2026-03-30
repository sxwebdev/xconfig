package testutil

import "testing"

func TestEqual_pass(t *testing.T) {
	Equal(t, 42, 42)
	Equal(t, "hello", "hello")
	Equal(t, []int{1, 2, 3}, []int{1, 2, 3})
	Equal(t, map[string]int{"a": 1}, map[string]int{"a": 1})
}

func TestEqual_fail(t *testing.T) {
	ft := &testing.T{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		Equal(ft, 1, 2)
	}()
	<-done

	if !ft.Failed() {
		t.Fatal("expected test to fail for non-equal values")
	}
}
