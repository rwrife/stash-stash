package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rwrife/stash-stash/internal/meta"
	"github.com/rwrife/stash-stash/internal/model"
	"github.com/rwrife/stash-stash/internal/theme"
)

func TestThemeFlagOverridesEnvironment(t *testing.T) {
	t.Setenv("STASH_STASH_THEME", "light")
	originalList, originalLoad, originalSet := gitList, metaLoad, setTUITheme
	gitList = func(context.Context) ([]model.Stash, error) { return nil, nil }
	metaLoad = func(context.Context) (*meta.Store, error) {
		return &meta.Store{Version: 1, Entries: map[string]meta.Entry{}}, nil
	}
	var selected string
	setTUITheme = func(palette theme.Palette) { selected = palette.Name }
	t.Cleanup(func() {
		gitList, metaLoad, setTUITheme = originalList, originalLoad, originalSet
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--theme", "dark"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if selected != "dark" {
		t.Fatalf("selected theme = %q, want flag value dark", selected)
	}
}

func TestThemeEnvironmentSelectsPalette(t *testing.T) {
	t.Setenv("STASH_STASH_THEME", "high-contrast")
	originalList, originalLoad, originalSet := gitList, metaLoad, setTUITheme
	gitList = func(context.Context) ([]model.Stash, error) { return nil, nil }
	metaLoad = func(context.Context) (*meta.Store, error) {
		return &meta.Store{Version: 1, Entries: map[string]meta.Entry{}}, nil
	}
	var selected string
	setTUITheme = func(palette theme.Palette) { selected = palette.Name }
	t.Cleanup(func() {
		gitList, metaLoad, setTUITheme = originalList, originalLoad, originalSet
	})

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if selected != "high-contrast" {
		t.Fatalf("selected theme = %q, want environment value", selected)
	}
}

func TestUnknownThemeListsValidNames(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--theme", "sepia"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	for _, name := range theme.Names() {
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("error %q does not list %q", stderr.String(), name)
		}
	}
}
