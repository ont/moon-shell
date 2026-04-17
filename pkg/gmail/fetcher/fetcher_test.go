package fetcher

import "testing"

func TestSubjectMatches(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
		ok   bool
	}{
		{name: "exact", got: "moon-shell", want: "moon-shell", ok: true},
		{name: "reply", got: "Re: moon-shell", want: "moon-shell", ok: true},
		{name: "nested prefixes", got: "Fwd: Re: moon-shell", want: "moon-shell", ok: true},
		{name: "suffix", got: "moon-shell 2", want: "moon-shell", ok: true},
		{name: "case insensitive", got: "MOON-SHELL", want: "moon-shell", ok: true},
		{name: "different subject", got: "other", want: "moon-shell", ok: false},
	}

	for _, tc := range cases {
		if got := subjectMatches(tc.got, tc.want); got != tc.ok {
			t.Fatalf("%s: subjectMatches(%q, %q) = %v, want %v", tc.name, tc.got, tc.want, got, tc.ok)
		}
	}
}
