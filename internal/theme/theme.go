// Package theme provides named color palettes for the terminal interface.
package theme

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette contains the terminal colors used by the TUI and its diff preview.
type Palette struct {
	Name               string
	Border             lipgloss.Color
	SelectedForeground lipgloss.Color
	SelectedBackground lipgloss.Color
	Text               lipgloss.Color
	Selection          lipgloss.Color
	Muted              lipgloss.Color
	Branch             lipgloss.Color
	Help               lipgloss.Color
	Error              lipgloss.Color
	Label              lipgloss.Color
	AutoLabel          lipgloss.Color
	Stat               lipgloss.Color
	Note               lipgloss.Color
	Edit               lipgloss.Color
	Confirm            lipgloss.Color
	Banner             lipgloss.Color
	Tag                lipgloss.Color
	AgeAging           lipgloss.Color
	AgeStale           lipgloss.Color
	AgeAncient         lipgloss.Color
	DiffAdded          lipgloss.Color
	DiffRemoved        lipgloss.Color
	DiffHunk           lipgloss.Color
	DiffHeader         lipgloss.Color
}

var palettes = map[string]Palette{
	"dark": {
		Name: "dark", Border: "240", SelectedForeground: "231", SelectedBackground: "63",
		Text: "252", Selection: "212", Muted: "244", Branch: "110", Help: "241",
		Error: "203", Label: "222", AutoLabel: "245", Stat: "108", Note: "180",
		Edit: "214", Confirm: "203", Banner: "214", Tag: "79", AgeAging: "179",
		AgeStale: "208", AgeAncient: "203", DiffAdded: "78", DiffRemoved: "203",
		DiffHunk: "75", DiffHeader: "252",
	},
	"light": {
		Name: "light", Border: "244", SelectedForeground: "15", SelectedBackground: "25",
		Text: "235", Selection: "127", Muted: "240", Branch: "25", Help: "238",
		Error: "160", Label: "130", AutoLabel: "245", Stat: "30", Note: "130",
		Edit: "130", Confirm: "160", Banner: "130", Tag: "30", AgeAging: "136",
		AgeStale: "166", AgeAncient: "160", DiffAdded: "28", DiffRemoved: "160",
		DiffHunk: "25", DiffHeader: "235",
	},
	"high-contrast": {
		Name: "high-contrast", Border: "15", SelectedForeground: "16", SelectedBackground: "226",
		Text: "15", Selection: "201", Muted: "15", Branch: "51", Help: "15",
		Error: "196", Label: "226", AutoLabel: "15", Stat: "51", Note: "226",
		Edit: "226", Confirm: "196", Banner: "226", Tag: "51", AgeAging: "226",
		AgeStale: "208", AgeAncient: "196", DiffAdded: "46", DiffRemoved: "196",
		DiffHunk: "51", DiffHeader: "15",
	},
}

// Default returns the dark palette used when no theme is selected.
func Default() Palette {
	return palettes["dark"]
}

// Names returns the supported theme names in display order.
func Names() []string {
	return []string{"dark", "light", "high-contrast"}
}

// Resolve returns a named palette or an error listing every supported name.
func Resolve(name string) (Palette, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	palette, ok := palettes[name]
	if !ok {
		return Palette{}, fmt.Errorf("unknown theme %q (valid themes: %s)", name, strings.Join(Names(), ", "))
	}
	return palette, nil
}
