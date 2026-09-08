package main

import "fmt"

type colorScheme struct {
	name                                             string
	top, topActive, menu, menuActive                 int
	text, accent, muted                              int
	keyword, stringValue, number, function, typeName int
	operator, parameter, comment, folder             int
}

var schemes = map[string]colorScheme{
	"plum":   {"Plum", 53, 95, 238, 242, 252, 213, 244, 213, 114, 81, 81, 220, 177, 215, 244, 183},
	"forest": {"Forest", 22, 28, 235, 240, 252, 150, 244, 150, 186, 117, 81, 223, 174, 216, 244, 108},
	"amber":  {"Amber", 58, 94, 236, 240, 253, 223, 244, 214, 150, 117, 221, 180, 203, 215, 244, 180},
	"mono":   {"Mono", 237, 245, 236, 250, 255, 252, 244, 255, 250, 252, 255, 253, 248, 255, 244, 250},
}

var colors = schemes["plum"]

func setColorScheme(name string) bool {
	scheme, ok := settings.themes[name]
	if ok {
		colors = scheme
	}
	return ok
}

func setThemeColor(name string, value int) error {
	return setSchemeColor(&colors, name, value)
}

func setSchemeColor(scheme *colorScheme, name string, value int) error {
	targets := map[string]*int{
		"top": &scheme.top, "top_active": &scheme.topActive, "menu": &scheme.menu, "menu_active": &scheme.menuActive,
		"text": &scheme.text, "accent": &scheme.accent, "muted": &scheme.muted, "keyword": &scheme.keyword,
		"string": &scheme.stringValue, "number": &scheme.number, "function": &scheme.function, "type": &scheme.typeName,
		"operator": &scheme.operator, "parameter": &scheme.parameter, "comment": &scheme.comment,
		"folder": &scheme.folder,
	}
	target, ok := targets[name]
	if !ok {
		return fmt.Errorf("unknown color %s", name)
	}
	*target = value
	return nil
}

func ansiFG(color int) string { return fmt.Sprintf("\x1b[38;5;%dm", color) }

func ansiBG(background, foreground int, attributes string) string {
	return fmt.Sprintf("\x1b[48;5;%d;38;5;%d;%sm", background, foreground, attributes)
}
