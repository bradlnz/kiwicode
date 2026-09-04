package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type editorSettings struct {
	bindings           map[rune]string
	terminalMaximized  bool
	explorerMaxPercent int
	topMenuPadding     int
	popupPadding       int
	tabPadding         int
	sidebarTabPadding  int
	explorerIndent     int
	icons              map[string]string
}

var settings editorSettings

var defaultShortcuts = map[string]rune{
	"explorer": 2, "copy": 3, "go-to-definition": 4, "test-explorer": 5, "search": 6,
	"graph": 7, "format": 11, "new": 14, "help": 15, "files-explorer": 16, "function-search": 21,
	"quit": 17, "diagnostics": 18, "save": 19, "terminal": 20, "paste": 22, "undo": 26,
	"close": 23,
}

var shortcutNames = map[string]string{
	"explorer": "Toggle Explorer", "copy": "Copy", "test-explorer": "Test Explorer",
	"search": "File Search", "graph": "Dependency Graph", "format": "Format", "new": "New File",
	"help": "Shortcut Help", "files-explorer": "Files Explorer", "quit": "Quit", "diagnostics": "Diagnostics",
	"save": "Save", "terminal": "Terminal", "paste": "Paste", "undo": "Undo", "close": "Close Tab",
	"function-search": "Function Search", "run": "Run Application", "test": "Run Unit Tests", "debug": "Debug",
	"inspect":          "Inspect Code",
	"go-to-definition": "Go to Definition",
	"architecture":     "Architecture Canvas", "slop": "AI Slop Scan",
	"word-wrap":        "Word Wrap",
	"source-control":   "Source Control",
	"source-stage-all": "Stage All", "source-commit": "Commit", "source-pull": "Git Pull", "source-push": "Git Push", "source-fetch": "Git Fetch",
}

func init() { resetSettings() }

func resetSettings() {
	colors = schemes["plum"]
	settings = editorSettings{
		bindings: map[rune]string{}, terminalMaximized: true, explorerMaxPercent: 50,
		topMenuPadding: 1, popupPadding: 1, tabPadding: 2, sidebarTabPadding: 2, explorerIndent: 2,
		icons: map[string]string{"files": "▤", "tests": "✓", "source": "⑂", "folder_open": "▾", "folder_closed": "▸", "test_checked": "☑", "test_unchecked": "☐"},
	}
	for action, key := range defaultShortcuts {
		settings.bindings[key] = action
	}
}

func loadSettings() error {
	resetSettings()
	paths := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "code-editor", "config.yaml"))
	}
	paths = append(paths, ".code-editor.yaml")
	for _, path := range paths {
		if err := readSettings(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func readSettings(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	section := ""
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("%s:%d: expected key: value", path, lineNumber)
		}
		key, value := strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if value == "" {
			section = key
			continue
		}
		if err := applySetting(section, key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
	}
	return scanner.Err()
}

func applySetting(section, key, value string) error {
	switch section {
	case "colors":
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 || number > 255 {
			return fmt.Errorf("color %s must be 0..255", key)
		}
		return setThemeColor(strings.ReplaceAll(key, "-", "_"), number)
	case "shortcuts":
		action := strings.ReplaceAll(key, "_", "-")
		if _, ok := shortcutNames[action]; !ok {
			return fmt.Errorf("unknown shortcut %s", key)
		}
		control, ok := parseControlKey(value)
		if !ok {
			return fmt.Errorf("shortcut %s must look like ctrl+a", key)
		}
		for runeKey, bound := range settings.bindings {
			if bound == action {
				delete(settings.bindings, runeKey)
			}
		}
		settings.bindings[control] = action
		return nil
	case "terminal":
		if key == "maximized" {
			settings.terminalMaximized = strings.EqualFold(value, "true")
			return nil
		}
	case "explorer":
		if key == "max_width_percent" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 20 || number > 70 {
				return fmt.Errorf("explorer max_width_percent must be 20..70")
			}
			settings.explorerMaxPercent = number
			return nil
		}
	case "layout":
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 || number > 8 {
			return fmt.Errorf("layout %s must be 0..8", key)
		}
		switch key {
		case "top_menu_padding":
			settings.topMenuPadding = number
		case "popup_padding":
			settings.popupPadding = number
		case "tab_padding":
			settings.tabPadding = number
		case "sidebar_tab_padding":
			settings.sidebarTabPadding = number
		case "explorer_indent":
			settings.explorerIndent = number
		default:
			return fmt.Errorf("unknown layout setting %s", key)
		}
		return nil
	case "icons":
		if _, ok := settings.icons[key]; !ok {
			return fmt.Errorf("unknown icon %s", key)
		}
		if value == "" {
			return fmt.Errorf("icon %s cannot be empty", key)
		}
		settings.icons[key] = value
		return nil
	case "":
		if key == "theme" {
			if !setColorScheme(strings.ToLower(value)) {
				return fmt.Errorf("unknown theme %s", value)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown setting %s.%s", section, key)
}

func parseControlKey(value string) (rune, bool) {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	if !strings.HasPrefix(value, "ctrl+") || len([]rune(value)) != 6 {
		return 0, false
	}
	letter := []rune(value)[5]
	return letter & 0x1f, letter >= 'a' && letter <= 'z' && letter != 'i' && letter != 'j' && letter != 'm'
}

func shortcutAction(key rune) string { return settings.bindings[key] }

func shortcutLabel(action string) string {
	for key, bound := range settings.bindings {
		if bound == action {
			return "Ctrl+" + strings.ToUpper(string(key+'a'-1))
		}
	}
	return ""
}

func shortcutHelpLines() []string {
	order := []string{"test-explorer", "source-control", "source-stage-all", "source-commit", "source-pull", "source-push", "source-fetch", "search", "function-search", "go-to-definition", "inspect", "terminal", "format", "run", "test", "debug", "diagnostics", "graph", "architecture", "files-explorer", "explorer", "new", "save", "undo", "close", "copy", "paste", "help", "quit"}
	lines := make([]string, 0, len(order))
	for _, action := range order {
		if label := shortcutLabel(action); label != "" {
			lines = append(lines, fmt.Sprintf("%-8s %s", label, shortcutNames[action]))
		}
	}
	return lines
}
