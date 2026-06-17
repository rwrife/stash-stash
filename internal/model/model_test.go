package model

import "testing"

func TestRef(t *testing.T) {
	cases := []struct {
		index int
		want  string
	}{
		{0, "stash@{0}"},
		{1, "stash@{1}"},
		{42, "stash@{42}"},
	}
	for _, c := range cases {
		s := Stash{Index: c.index}
		if got := s.Ref(); got != c.want {
			t.Errorf("Stash{Index:%d}.Ref() = %q, want %q", c.index, got, c.want)
		}
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 7: "7", 10: "10", 123: "123", -5: "-5"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}
