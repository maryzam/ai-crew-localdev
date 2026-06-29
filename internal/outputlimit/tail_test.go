package outputlimit

import (
	"reflect"
	"testing"
)

func TestBufferKeepsBoundedTail(t *testing.T) {
	buffer := New(8)
	_, _ = buffer.Write([]byte("one\ntwo\nthree\n"))
	if got := string(buffer.Bytes()); got != "o\nthree\n" {
		t.Fatalf("bytes = %q", got)
	}
	if !buffer.Truncated() {
		t.Fatal("expected truncation")
	}
	if got := buffer.LastLines(1, 8); !reflect.DeepEqual(got, []string{"three"}) {
		t.Fatalf("lines = %#v", got)
	}
}
