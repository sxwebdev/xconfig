package f

import (
	"testing"

	"github.com/sxwebdev/xconfig/internal/testutil"
)

func TestUnmarshalerStringSlice(t *testing.T) {
	expect := TextUnmarshalerStringSlice{"a", "b", "c"}
	value := TextUnmarshalerStringSlice{}

	err := value.UnmarshalText([]byte("a.b.c"))
	if err != nil {
		t.Error(err)
	}

	testutil.Equal(t, expect, value)
}
