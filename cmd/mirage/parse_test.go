package main

import (
	"flag"
	"reflect"
	"testing"
)

// TestParseMixed covers the flag/positional ordering that broke `start base
// --gui` (Go's flag package stops at the first positional).
func TestParseMixed(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPos  []string
		wantGUI  bool
		wantTool string
	}{
		{"positional first", []string{"base", "--gui", "--tools", "x.img"}, []string{"base"}, true, "x.img"},
		{"flags first", []string{"--gui", "--tools", "x.img", "base"}, []string{"base"}, true, "x.img"},
		{"interspersed", []string{"--gui", "base", "--tools", "x.img"}, []string{"base"}, true, "x.img"},
		{"value flag before positional", []string{"--tools", "x.img", "base"}, []string{"base"}, false, "x.img"},
		{"no flags", []string{"base"}, []string{"base"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("start", flag.ContinueOnError)
			gui := fs.Bool("gui", false, "")
			tools := fs.String("tools", "", "")
			pos, err := parseMixed(fs, tc.args)
			if err != nil {
				t.Fatalf("parseMixed error: %v", err)
			}
			if !reflect.DeepEqual(pos, tc.wantPos) {
				t.Errorf("positionals = %v, want %v", pos, tc.wantPos)
			}
			if *gui != tc.wantGUI {
				t.Errorf("gui = %v, want %v", *gui, tc.wantGUI)
			}
			if *tools != tc.wantTool {
				t.Errorf("tools = %q, want %q", *tools, tc.wantTool)
			}
		})
	}
}
