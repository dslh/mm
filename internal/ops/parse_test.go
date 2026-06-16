package ops

import (
	"strings"
	"testing"
)

func TestParseApplyLinesJSONL(t *testing.T) {
	in := `
# a comment
{"query":"tomate","n":2}

{"id":"abc","set":0}
# trailing comment
`
	lines, err := ParseApplyLines(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if lines[0].Query != "tomate" || lines[0].N == nil || *lines[0].N != 2 {
		t.Errorf("line 0 = %+v", lines[0])
	}
	if lines[1].ID != "abc" || lines[1].Set == nil || *lines[1].Set != 0 {
		t.Errorf("line 1 = %+v", lines[1])
	}
}

func TestParseApplyLinesJSONArray(t *testing.T) {
	in := `[{"query":"lait","n":1},{"id":"x","set":3}]`
	lines, err := ParseApplyLines(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Query != "lait" || lines[1].ID != "x" {
		t.Errorf("lines = %+v", lines)
	}
}

func TestParseApplyLinesEmpty(t *testing.T) {
	if _, err := ParseApplyLines(strings.NewReader("   \n  ")); err == nil {
		t.Error("want error for empty input")
	}
}

func TestParseApplyLinesBadLine(t *testing.T) {
	_, err := ParseApplyLines(strings.NewReader(`{"query":"ok"}` + "\n" + `{not json`))
	if err == nil {
		t.Fatal("want error for malformed line")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should name the bad line: %v", err)
	}
}

func TestParseApplyLinesBadArray(t *testing.T) {
	if _, err := ParseApplyLines(strings.NewReader(`[{"query":}]`)); err == nil {
		t.Error("want error for malformed array")
	}
}
