package main

import "testing"

func TestDarkGTKTheme(t *testing.T) {
	// A table-driven test: each row is one case, and the loop below runs
	// them all. t.Run gives each case its own name in the output.
	tests := []struct {
		name  string
		style desktopStyle
		env   string // the GTK_THEME environment variable
		want  string
	}{
		{"light desktop", desktopStyle{prefersDark: false, gtkTheme: "Adwaita"}, "", ""},
		{"dark desktop", desktopStyle{prefersDark: true, gtkTheme: "Adwaita"}, "", "Adwaita:dark"},
		{"other theme", desktopStyle{prefersDark: true, gtkTheme: "Yaru"}, "", "Yaru:dark"},
		{"theme already dark", desktopStyle{prefersDark: true, gtkTheme: "Yaru-dark"}, "", ""},
		{"theme name unknown", desktopStyle{prefersDark: true}, "", ""},
		{"user set GTK_THEME", desktopStyle{prefersDark: true, gtkTheme: "Adwaita"}, "HighContrast", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := darkGTKTheme(tt.style, tt.env); got != tt.want {
				t.Errorf("darkGTKTheme(%+v, %q) = %q, want %q", tt.style, tt.env, got, tt.want)
			}
		})
	}
}

func TestIsDarkThemeName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Adwaita", false},
		{"Adwaita-dark", true},
		{"Adwaita:dark", true},
		{"Breeze-Dark", true},
		{"Darkness", false}, // "dark" only counts as a variant suffix
		{"", false},
	}
	for _, tt := range tests {
		if got := isDarkThemeName(tt.name); got != tt.want {
			t.Errorf("isDarkThemeName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCSSColor(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"GNOME red", []any{0.902, 0.176, 0.259}, "rgb(230 45 66)"},
		{"pure green", []any{0.0, 1.0, 0.0}, "rgb(0 255 0)"},
		{"unset (out of range)", []any{-1.0, -1.0, -1.0}, ""},
		{"too few fields", []any{0.5, 0.5}, ""},
		{"wrong type", "blue", ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cssColor(tt.in); got != tt.want {
				t.Errorf("cssColor(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
