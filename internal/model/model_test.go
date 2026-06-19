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

func TestDisplayPrefersLabel(t *testing.T) {
	labeled := Stash{Subject: "WIP on main: raw", Label: "payments: retry fix"}
	if got := labeled.Display(); got != "payments: retry fix" {
		t.Errorf("Display() with label = %q, want the label", got)
	}
	unlabeled := Stash{Subject: "WIP on main: raw"}
	if got := unlabeled.Display(); got != "WIP on main: raw" {
		t.Errorf("Display() without label = %q, want the subject", got)
	}
}

func TestDiffstatString(t *testing.T) {
	cases := []struct {
		name string
		ds   Diffstat
		want string
	}{
		{"zero", Diffstat{}, ""},
		{"typical", Diffstat{Added: 12, Deleted: 3, Files: 2}, "+12 -3 · 2 files"},
		{"single file", Diffstat{Added: 1, Deleted: 0, Files: 1}, "+1 -0 · 1 file"},
		{"binary only", Diffstat{Files: 1, Binary: true}, "1 file · bin"},
		{"text plus binary", Diffstat{Added: 4, Deleted: 1, Files: 2, Binary: true}, "+4 -1 bin · 2 files"},
	}
	for _, c := range cases {
		if got := c.ds.String(); got != c.want {
			t.Errorf("%s: Diffstat.String() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDiffstatIsZero(t *testing.T) {
	if !(Diffstat{}).IsZero() {
		t.Error("empty Diffstat.IsZero() = false")
	}
	if (Diffstat{Files: 1}).IsZero() {
		t.Error("non-empty Diffstat.IsZero() = true")
	}
}
