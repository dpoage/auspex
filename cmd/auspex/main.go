// Command auspex runs the mechanical checks of an oracle-gated development
// round: the checks with a deterministic answer. Judgment stays with the
// agents.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/dpoage/auspex/internal/evidence"
	"github.com/dpoage/auspex/internal/ledger"
	"github.com/dpoage/auspex/internal/repo"
	"github.com/dpoage/llmkit/decide"
	"github.com/dpoage/llmkit/sandbox"
)

// command is one registered subcommand. Each command file's init calls
// register exactly once; main.go is never edited to add a command.
type command struct {
	name, synopsis string
	run            func(args []string, stdout, stderr io.Writer) int
}

var commands []command

// designOrder is every command name in DESIGN.md's order; help lists the
// registered commands in this order.
var designOrder = map[string]int{
	"observe":    0,
	"appoint":    1,
	"heed":       2,
	"decree":     3,
	"omen":       4,
	"inaugurate": 5,
	"read":       6,
	"augury":     7,
}

func register(c command) {
	commands = append(commands, c)
}

func commandNamed(name string) *command {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

// Exit codes: 0 success; 1 obnuntiatio (a gate failed, or any error on a
// gate path); 2 usage error (usage on stderr).
const (
	exitSuccess     = 0
	exitObnuntiatio = 1
	exitUsage       = 2
)

func usage(w io.Writer) int {
	order := append([]command(nil), commands...)
	sort.SliceStable(order, func(i, j int) bool {
		return designOrder[order[i].name] < designOrder[order[j].name]
	})
	fmt.Fprintln(w, "auspex: the mechanical checks of an oracle-gated round")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, c := range order {
		fmt.Fprintf(w, "  %-28s %s\n", c.name, c.synopsis)
	}
	return exitUsage
}

// prospera prints a passed gate's headline.
func prospera(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "auspicia prospera: "+format+"\n", a...)
}

// obnuntiatio prints a failed gate: the headline, then each finding indented
// two spaces, and returns 1.
func obnuntiatio(w io.Writer, headline string, lines []string) int {
	fmt.Fprintf(w, "obnuntiatio: %s\n", headline)
	for _, line := range lines {
		fmt.Fprintf(w, "  %s\n", line)
	}
	return exitObnuntiatio
}

func openLedger() (*ledger.Ledger, error) {
	return ledger.New(".", time.Now), nil
}

func openEvidence() (*evidence.Store, error) {
	return evidence.Open()
}

func openRepo() (*repo.Repo, error) {
	return repo.Open(".")
}

// newSandbox returns the sandbox backend and its name, which observe
// records in every Leg.Backend.
func newSandbox() (sandbox.Sandbox, string) {
	return sandbox.NewHostExec(), "host"
}

// newJudge returns the TypeSafe Jev client for the judge lints. It fails
// closed when LLMKIT_TYPESAFE_API_KEY is unset.
func newJudge() (*decide.Client, error) {
	key := os.Getenv("LLMKIT_TYPESAFE_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("LLMKIT_TYPESAFE_API_KEY is not set")
	}
	return decide.New(decide.Config{APIKey: key, Model: "jev-latest"})
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	name, rest := args[0], args[1:]
	if name == "help" || name == "-h" || name == "--help" {
		usage(stdout)
		return exitSuccess
	}
	c := commandNamed(name)
	if c == nil {
		fmt.Fprintf(stderr, "auspex: unknown command %q\n", name)
		return usage(stderr)
	}
	return c.run(rest, stdout, stderr)
}
