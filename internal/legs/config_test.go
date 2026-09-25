package legs

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// gleeunit's reporting.gleam finished/1 wraps its whole count line in one
// SGR color, never the digits themselves: green for an all-pass run, red
// when there are failures, yellow when there are only skips.
func green(msg string) string  { return "\x1b[32m" + msg + "\x1b[39m" }
func red(msg string) string    { return "\x1b[31m" + msg + "\x1b[39m" }
func yellow(msg string) string { return "\x1b[33m" + msg + "\x1b[39m" }

// TestGuitartimeConfigLoadsAndCounts is criterion G8: the shipped guitartime
// config loads, its count regex sums passed+failures (never skipped) across
// every match, and its module entries cover shared/, server/, and web/.
func TestGuitartimeConfigLoadsAndCounts(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("testdata", "guitartime.auspex.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	checkTogether := green("\n569 passed, no failures") +
		green("\n569 passed, no failures") +
		green("\n701 passed, no failures") +
		green("\n1357 passed, no failures")
	if n, counted := countExecuted(cfg.CountRegex, checkTogether); !counted || n != 3196 {
		t.Fatalf("aggregate count = %d, counted=%v; want 3196, true", n, counted)
	}

	withFailure := red("\n568 passed, 1 failures")
	if n, counted := countExecuted(cfg.CountRegex, withFailure); !counted || n != 569 {
		t.Fatalf("count with a failure = %d, counted=%v; want 569, true", n, counted)
	}

	withSkips := yellow("\n560 passed, 0 failures, 9 skipped")
	if n, counted := countExecuted(cfg.CountRegex, withSkips); !counted || n != 560 {
		t.Fatalf("count with skips = %d, counted=%v; want 560, true (skipped must not be captured)", n, counted)
	}

	for _, tc := range []struct {
		test   string
		prefix string
	}{
		{"shared/test/woodshed_shared/foo_test.gleam", "shared/"},
		{"server/test/woodshed_server/service/today_test.gleam", "server/"},
		{"web/test/woodshed_web/page_test.gleam", "web/"},
	} {
		cmd, err := cfg.ModuleFor([]string{tc.test})
		if err != nil {
			t.Fatalf("ModuleFor(%q): %v", tc.test, err)
		}
		want := cfg.longestMatch(tc.test)
		if want == nil || want.Prefix != tc.prefix {
			t.Fatalf("ModuleFor(%q) resolved prefix = %v, want %q", tc.test, want, tc.prefix)
		}
		if len(cmd.Argv) == 0 || cmd.Timeout <= 0 {
			t.Fatalf("ModuleFor(%q) command incomplete: %+v", tc.test, cmd)
		}
	}
}

// TestLoadConfigRequiresEveryTimeout is part of criterion G5: a command
// (suite or a module entry) with no timeout is a config-load error, not a
// silently defaulted zero timeout (which would let HostExec never kill it).
func TestLoadConfigRequiresEveryTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.toml")
	body := `count_regex = '(\d+) ok'

[suite]
argv = ["true"]
timeout = "1m"

[[module]]
prefix = "pkg/"
argv = ["true"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig with a module entry missing its timeout: want an error, got nil")
	}
}

// TestLoadConfigMissingFileErrors is part of criterion G5: a missing config
// file is an error, not a silently empty configuration.
func TestLoadConfigMissingFileErrors(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.toml")); err == nil {
		t.Fatal("LoadConfig of a missing file: want an error, got nil")
	}
}

// TestModuleForRefusesMixedOrUnmatchedTests is part of criterion G5: a test
// path matching no module entry, test paths split across more than one
// entry, and a non-canonical test path, which can climb out of the tree
// whatever prefix it starts with, are all errors. Hiding mutant: drop the
// canonical-path check.
func TestModuleForRefusesMixedOrUnmatchedTests(t *testing.T) {
	cfg := Config{
		Modules: []ModuleEntry{
			{Prefix: "a/", Command: Command{Argv: []string{"true"}, Timeout: 1}},
			{Prefix: "b/", Command: Command{Argv: []string{"true"}, Timeout: 1}},
		},
	}
	if _, err := cfg.ModuleFor([]string{"c/x_test.go"}); err == nil {
		t.Fatal("ModuleFor with no matching entry: want an error, got nil")
	}
	if _, err := cfg.ModuleFor([]string{"a/x_test.go", "b/y_test.go"}); err == nil {
		t.Fatal("ModuleFor with tests split across two entries: want an error, got nil")
	}
	if _, err := cfg.ModuleFor([]string{"a/../../x_test.go"}); err == nil {
		t.Fatal("ModuleFor with a non-canonical test path: want an error, got nil")
	}
}

// TestModuleForMatchesWholeComponentsLongestFirst is F8: a prefix matches a
// test path equal to it or beneath it by whole path components, so
// "server" does not claim "server2/..."; among matching prefixes the
// longest wins whatever the entry order. Hiding mutants: a plain string
// prefix; the shortest prefix wins.
func TestModuleForMatchesWholeComponentsLongestFirst(t *testing.T) {
	cmd := func(name string) Command { return Command{Argv: []string{name}, Timeout: time.Minute} }
	server := Config{Modules: []ModuleEntry{{Prefix: "server", Command: cmd("server")}}}
	if got, err := server.ModuleFor([]string{"server2/test/x_test.gleam"}); err == nil {
		t.Fatalf("prefix \"server\" claimed server2/test/x_test.gleam (command %v), want no match", got.Argv)
	}
	if got, err := server.ModuleFor([]string{"server/test/x_test.gleam"}); err != nil || got.Argv[0] != "server" {
		t.Fatalf("prefix \"server\" on server/test/x_test.gleam: %v, %v; want the server command", got.Argv, err)
	}

	outer := ModuleEntry{Prefix: "src/", Command: cmd("outer")}
	inner := ModuleEntry{Prefix: "src/pkg/", Command: cmd("inner")}
	for _, modules := range [][]ModuleEntry{{outer, inner}, {inner, outer}} {
		c := Config{Modules: modules}
		if got, err := c.ModuleFor([]string{"src/pkg/x_test.go"}); err != nil || got.Argv[0] != "inner" {
			t.Fatalf("entries %q, %q: src/pkg/x_test.go -> %v, %v; want inner", modules[0].Prefix, modules[1].Prefix, got.Argv, err)
		}
		if got, err := c.ModuleFor([]string{"src/other/x_test.go"}); err != nil || got.Argv[0] != "outer" {
			t.Fatalf("entries %q, %q: src/other/x_test.go -> %v, %v; want outer", modules[0].Prefix, modules[1].Prefix, got.Argv, err)
		}
	}
}

// TestLoadConfigRequiresCaptureGroup is F6: a count_regex with no capture
// group has nothing to sum and is a config-load error. Hiding mutant: drop
// the capture-group check.
func TestLoadConfigRequiresCaptureGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.toml")
	body := `count_regex = '\d+ ok'

[suite]
argv = ["true"]
timeout = "1m"

[[module]]
prefix = "pkg/"
argv = ["true"]
timeout = "1m"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig with a count_regex that has no capture group: want an error, got nil")
	}
}

// TestCountExecutedRefusesUnreadableCaptures is F6: a capture that is not a
// decimal number, one past the int range, or a sum past it leaves the text
// uncounted rather than counting less than it says. Hiding mutants: skip an
// unparsable capture; drop the sum-overflow check.
func TestCountExecutedRefusesUnreadableCaptures(t *testing.T) {
	for _, tc := range []struct{ name, re, text string }{
		{"thousands separator", `([\d,]+) passed`, "1,357 passed"},
		{"capture past the int range", `(\d+) ok`, "99999999999999999999 ok"},
		{"sum past the int range", `(\d+) ok`, strconv.Itoa(math.MaxInt) + " ok\n1 ok"},
	} {
		if n, counted := countExecuted(regexp.MustCompile(tc.re), tc.text); counted {
			t.Errorf("%s: countExecuted = %d, true; want uncounted", tc.name, n)
		}
	}
}
