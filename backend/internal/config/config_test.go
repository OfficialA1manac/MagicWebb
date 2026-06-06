package config

import "testing"

func TestParseAddrList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"0xAbC", []string{"0xabc"}},
		{"0xAbC,0xDeF", []string{"0xabc", "0xdef"}},
		{" 0xAbC , ,0xDeF ", []string{"0xabc", "0xdef"}}, // trims, lowercases, drops blanks
		{",,", nil},
	}
	for _, tc := range cases {
		got := parseAddrList(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("parseAddrList(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseAddrList(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestIsAdmin(t *testing.T) {
	c := &Config{AdminAllowlist: parseAddrList("0xAAA,0xBBB")}

	if !c.IsAdmin("0xaaa") {
		t.Fatal("0xaaa should be admin")
	}
	if !c.IsAdmin("  0xBBB  ") { // case-insensitive + trimmed
		t.Fatal("0xBBB should be admin regardless of case/whitespace")
	}
	if c.IsAdmin("0xccc") {
		t.Fatal("0xccc must not be admin")
	}
	if c.IsAdmin("") {
		t.Fatal("empty address must not be admin")
	}

	empty := &Config{}
	if empty.IsAdmin("0xaaa") {
		t.Fatal("empty allowlist must admit no one")
	}
}
