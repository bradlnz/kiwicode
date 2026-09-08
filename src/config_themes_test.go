package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestYAMLThemePalettes(t *testing.T) {
	oldSettings, oldColors := settings, colors
	t.Cleanup(func() { settings, colors = oldSettings, oldColors })
	resetSettings()
	if err := readSettings(".code-editor.yaml"); err != nil {
		t.Fatal(err)
	}
	for name, want := range schemes {
		if !setColorScheme(name) || colors != want {
			t.Fatalf("%s YAML palette differs from built-in: %+v", name, colors)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("themes:\n  forest:\n    folder: 200\n    parameter: 201\ntheme: forest\nlayout:\n  top_menu_padding: 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := readSettings(path); err != nil {
		t.Fatal(err)
	}
	if colors.folder != 200 || colors.parameter != 201 || settings.topMenuPadding != 3 {
		t.Fatal("nested palette or top-level theme not applied")
	}
	setColorScheme("plum")
	setColorScheme("forest")
	if colors.folder != 200 {
		t.Fatal("View theme switching lost YAML palette")
	}
	if err := applySetting("themes.forest", "folder", "256"); err == nil {
		t.Fatal("invalid palette accepted")
	}
	if err := applySetting("themes.forest", "unknown", "1"); err == nil {
		t.Fatal("unknown role accepted")
	}
	resetSettings()
	setColorScheme("forest")
	if colors != schemes["forest"] {
		t.Fatal("palette leaked into a new project's defaults")
	}
}
