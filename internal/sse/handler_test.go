package sse

import "testing"

// U1 remediation (specs/001-kiss-chess-engine — see /speckit-analyze pass 2):
// a malformed or non-numeric Last-Event-ID header MUST be treated as "no
// header" (caller sends fresh hydration), NEVER as a 500. The handler
// dispatches on the (uint64, bool) return — false → no replay header.
func TestParseLastEventID_Malformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"alpha", "abc"},
		{"mixed", "12abc"},
		{"trailing junk", "42x"},
		{"negative", "-7"},
		{"hex", "0xff"},
		{"huge non-numeric", strings("z", 65)},
		{"spaces", "  12  "},
		{"comma", "1,2,3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseLastEventID(tc.in)
			if ok {
				t.Errorf("parseLastEventID(%q) = (%d, true), want (_, false)", tc.in, got)
			}
		})
	}
}

func TestParseLastEventID_ValidPositive(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"18446744073709551615", 18446744073709551615}, // uint64 max
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseLastEventID(tc.in)
			if !ok {
				t.Fatalf("parseLastEventID(%q) reported invalid; want valid", tc.in)
			}
			if got != tc.want {
				t.Errorf("parseLastEventID(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// strings(c, n) builds a string of n copies of c without importing strings.
func strings(c string, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = c[0]
	}
	return string(out)
}
