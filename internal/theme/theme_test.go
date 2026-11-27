package theme

import (
	"strings"
	"testing"
)

func TestResolveThemes(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			palette, err := Resolve(name)
			if err != nil {
				t.Fatal(err)
			}
			if palette.Name != name {
				t.Fatalf("palette name = %q, want %q", palette.Name, name)
			}
		})
	}
}

func TestResolveUnknownTheme(t *testing.T) {
	_, err := Resolve("sepia")
	if err == nil {
		t.Fatal("Resolve(sepia) succeeded, want an error")
	}
	for _, name := range Names() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not list theme %q", err, name)
		}
	}
}
