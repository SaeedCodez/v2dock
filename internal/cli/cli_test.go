package cli

import (
	"bytes"
	"fmt"
	"testing"
)

func TestSafeWriterStripsEscapes(t *testing.T) {
	var b bytes.Buffer
	fmt.Fprintf(safeWriter{&b}, "name: %s\n", "evil\x1b]0;title\x07‮abc")
	if got := b.String(); got != "name: evil?]0;title??abc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"10GB": 10 << 30, "500mb": 500 << 20, "1.5G": 3 << 29, "0": 0, "unlimited": 0, "2048": 2048}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("%s: got %d, %v", in, got, err)
		}
	}
	if _, err := parseSize("ten"); err == nil {
		t.Error("accepted garbage")
	}
}

func TestArgsAnywhere(t *testing.T) {
	a, err := parseArgs([]string{"route", "--json", "add", "web", "--quota=5GB", "--period", "daily"})
	if err != nil {
		t.Fatal(err)
	}
	if a.arg(0) != "route" || a.arg(1) != "add" || a.arg(2) != "web" || !a.bools["json"] || a.flags["quota"] != "5GB" || a.flags["period"] != "daily" {
		t.Fatalf("%+v", a)
	}
}
