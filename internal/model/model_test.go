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

// --- issue #7: auto-label fallback + source ------------------------------

func TestDisplaySourcePrecedence(t *testing.T) {
	// User label wins over everything (even when an auto-label could be built).
	user := Stash{Subject: "WIP on main: raw", Label: "my name", Branch: "feature/x", TopFile: "a.go"}
	if got, src := user.DisplaySource(); got != "my name" || src != LabelUser {
		t.Errorf("DisplaySource() user = (%q,%v), want (\"my name\",LabelUser)", got, src)
	}

	// No user label but branch+file present → auto-label.
	auto := Stash{Subject: "WIP on feature/payments: raw", Branch: "feature/payments", TopFile: "internal/retry.go"}
	if got, src := auto.DisplaySource(); got != "payments: retry" || src != LabelAuto {
		t.Errorf("DisplaySource() auto = (%q,%v), want (\"payments: retry\",LabelAuto)", got, src)
	}

	// Nothing to derive from → raw subject.
	none := Stash{Subject: "WIP on main: raw"}
	if got, src := none.DisplaySource(); got != "WIP on main: raw" || src != LabelNone {
		t.Errorf("DisplaySource() none = (%q,%v), want (subject,LabelNone)", got, src)
	}
}

func TestAutoLabelIsDerivedNotPersisted(t *testing.T) {
	// AutoLabel reflects branch+file even when a user label is set (so callers
	// can surface the guess); it never reads Label.
	s := Stash{Label: "explicit", Branch: "fix/cache", TopFile: "store.go"}
	if got := s.AutoLabel(); got != "cache: store" {
		t.Errorf("AutoLabel() = %q, want \"cache: store\"", got)
	}
	// Display still prefers the explicit label.
	if got := s.Display(); got != "explicit" {
		t.Errorf("Display() = %q, want the explicit user label", got)
	}
}

func TestLabelSourceString(t *testing.T) {
	cases := map[LabelSource]string{LabelNone: "subject", LabelUser: "user", LabelAuto: "auto"}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("LabelSource(%d).String() = %q, want %q", k, got, want)
		}
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
