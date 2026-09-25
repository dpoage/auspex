// Package legs is the mutant-leg protocol: preparing a tree per leg,
// applying a mutant, restoring test paths from base, running the suite or
// module command through a sandbox, parsing the executed-test count, and
// the repository config that names the commands and the count regex.
package legs

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Command is one command auspex runs during a leg: its argv and the hard
// timeout applied as sandbox.Spec.Timeout. There is no placeholder
// substitution: argv is used verbatim.
type Command struct {
	Argv    []string
	Timeout time.Duration
}

// ModuleEntry maps one path prefix to the command that tests under it run
// with. Prefix is a repository-relative path, with or without a trailing
// slash, and matches whole path components: a test path equal to it or
// beneath it. The longest matching prefix wins when more than one entry
// matches a test path.
type ModuleEntry struct {
	Prefix  string
	Command Command
}

// Config is the decoded .agents/auspex.toml: the full-suite command, the
// executed-test count regex, and the module entries. Every command
// (suite and every module) carries a required, positive timeout.
type Config struct {
	CountRegex *regexp.Regexp
	Suite      Command
	Modules    []ModuleEntry
}

// rawConfig is the literal TOML shape; buildConfig turns it into a
// validated Config with compiled/parsed fields.
type rawConfig struct {
	CountRegex string      `toml:"count_regex"`
	Suite      rawCommand  `toml:"suite"`
	Module     []rawModule `toml:"module"`
}

type rawCommand struct {
	Argv    []string `toml:"argv"`
	Timeout string   `toml:"timeout"`
}

type rawModule struct {
	Prefix  string   `toml:"prefix"`
	Argv    []string `toml:"argv"`
	Timeout string   `toml:"timeout"`
}

// LoadConfig reads and validates the config at path. A missing file, an
// unparsable regex, a regex with no capture group, a command with no argv,
// or a command with no positive timeout is an error.
func LoadConfig(path string) (Config, error) {
	var raw rawConfig
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return Config{}, fmt.Errorf("legs: load config %s: %w", path, err)
	}
	return buildConfig(raw)
}

func buildConfig(raw rawConfig) (Config, error) {
	if strings.TrimSpace(raw.CountRegex) == "" {
		return Config{}, fmt.Errorf("legs: config: count_regex is required")
	}
	re, err := regexp.Compile(raw.CountRegex)
	if err != nil {
		return Config{}, fmt.Errorf("legs: config: count_regex: %w", err)
	}
	if re.NumSubexp() == 0 {
		return Config{}, fmt.Errorf("legs: config: count_regex has no capture group to count")
	}
	suite, err := buildCommand("suite", raw.Suite)
	if err != nil {
		return Config{}, err
	}
	if len(raw.Module) == 0 {
		return Config{}, fmt.Errorf("legs: config: at least one [[module]] entry is required")
	}
	modules := make([]ModuleEntry, len(raw.Module))
	seen := make(map[string]bool, len(raw.Module))
	for i, m := range raw.Module {
		if strings.TrimSpace(m.Prefix) == "" {
			return Config{}, fmt.Errorf("legs: config: module %d: prefix is required", i)
		}
		prefix := strings.TrimSuffix(m.Prefix, "/")
		if seen[prefix] {
			return Config{}, fmt.Errorf("legs: config: duplicate module prefix %q", m.Prefix)
		}
		seen[prefix] = true
		cmd, err := buildCommand(fmt.Sprintf("module %q", m.Prefix), rawCommand{Argv: m.Argv, Timeout: m.Timeout})
		if err != nil {
			return Config{}, err
		}
		modules[i] = ModuleEntry{Prefix: m.Prefix, Command: cmd}
	}
	return Config{CountRegex: re, Suite: suite, Modules: modules}, nil
}

func buildCommand(name string, raw rawCommand) (Command, error) {
	if len(raw.Argv) == 0 {
		return Command{}, fmt.Errorf("legs: config: %s: argv is required", name)
	}
	if strings.TrimSpace(raw.Timeout) == "" {
		return Command{}, fmt.Errorf("legs: config: %s: timeout is required", name)
	}
	d, err := time.ParseDuration(raw.Timeout)
	if err != nil {
		return Command{}, fmt.Errorf("legs: config: %s: timeout: %w", name, err)
	}
	if d <= 0 {
		return Command{}, fmt.Errorf("legs: config: %s: timeout must be positive", name)
	}
	return Command{Argv: append([]string(nil), raw.Argv...), Timeout: d}, nil
}

// ModuleFor returns the command every test path in tests shares, chosen by
// the longest matching prefix. A test path that is not canonical, a test
// path matching no entry, or test paths that resolve to different entries,
// is an error.
func (c Config) ModuleFor(tests []string) (Command, error) {
	if len(tests) == 0 {
		return Command{}, fmt.Errorf("legs: no test paths given")
	}
	var chosen *ModuleEntry
	for _, t := range tests {
		if !canonicalPath(t) {
			return Command{}, fmt.Errorf("legs: test path %q is not a canonical repository-relative path", t)
		}
		e := c.longestMatch(t)
		if e == nil {
			return Command{}, fmt.Errorf("legs: test path %q matches no module entry", t)
		}
		if chosen == nil {
			chosen = e
		} else if chosen.Prefix != e.Prefix {
			return Command{}, fmt.Errorf("legs: test paths map to different module entries (%q and %q)", chosen.Prefix, e.Prefix)
		}
	}
	return chosen.Command, nil
}

func (c Config) longestMatch(test string) *ModuleEntry {
	var best *ModuleEntry
	bestLen := 0
	for i := range c.Modules {
		m := &c.Modules[i]
		p := strings.TrimSuffix(m.Prefix, "/")
		if (test == p || strings.HasPrefix(test, p+"/")) && (best == nil || len(p) > bestLen) {
			best, bestLen = m, len(p)
		}
	}
	return best
}

// canonicalPath reports whether p is a canonical repository-relative path:
// not empty, ".", or absolute, and with no "..", ".", or empty component.
func canonicalPath(p string) bool {
	return p != "" && p != "." && p != ".." && !path.IsAbs(p) && path.Clean(p) == p && !strings.HasPrefix(p, "../")
}

// countExecuted is the executed-test count of text: the sum of every
// numeric capture group over every match of re, or (0, false) when re
// matches nothing at all. An unmatched optional group contributes nothing,
// which is how a "no failures"-style alternative sums to the same total as
// an explicit zero. A capture that is not a non-negative decimal number,
// or a sum past the int range, makes the count unreadable: (0, false).
func countExecuted(re *regexp.Regexp, text string) (int, bool) {
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	total := 0
	for _, m := range matches {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			v, err := strconv.Atoi(g)
			if err != nil || v < 0 || v > math.MaxInt-total {
				return 0, false
			}
			total += v
		}
	}
	return total, true
}
