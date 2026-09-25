# auspex

auspex is a Go CLI that runs the mechanical checks of an oracle-gated development
round. It runs every check with a deterministic answer and leaves judgment to the
agents.

A script measures cheaply and cannot invent output. The skills that describe the
round answer agent misreporting by adding a second agent to measure the first.

## Requirements

- Go 1.25 or newer, to build from source.
- `git`. `auspex observe` runs test legs at git revisions.
- [`bd`](https://github.com/gastownhall/beads). auspex stores every record in the
  beads database of the current directory.
- `LLMKIT_TYPESAFE_API_KEY`. Only `auspex omen` needs it.

## Install

```bash
go install github.com/dpoage/auspex/cmd/auspex@latest
```

The `auspex` binary lands in `$(go env GOPATH)/bin`.

## Quick start

auspex reads and writes the beads database of the current directory. Create that
database before the first command.

```bash
$ bd init

$ bd create -t task "slice one"
tmp_sXR8dPk0FN-bh2

$ auspex appoint --slice tmp_sXR8dPk0FN-bh2 --round demo.1 --tier standard
tmp_sXR8dPk0FN-bh2/6fa4fa591d49

$ auspex read tmp_sXR8dPk0FN-bh2/6fa4fa591d49
record tmp_sXR8dPk0FN-bh2/6fa4fa591d49
slice tmp_sXR8dPk0FN-bh2
at 2026-09-25T18:32:32Z
kind appointment
round demo.1
tier standard
merge-hash

$ auspex inaugurate cp2 --round demo.1
obnuntiatio: cp2 fails for round demo.1
  slice tmp_sXR8dPk0FN-bh2: has no appointment with a merge hash
$ echo $?
1
```

`auspex inaugurate` exits 1 because the appointment has no merge hash. Pass
`--merge-hash` to `auspex appoint` to record one.

## Commands

| Command | What it does |
| --- | --- |
| `auspex observe` | Run the mutant legs for one or more mutant/test pairs. |
| `auspex appoint` | Record a slice tier and merge hash, and add the slice label. |
| `auspex heed` | Record an oracle verdict from a transcript. |
| `auspex decree` | Record a ruling on a scope item, triage, deviation, or contract. |
| `auspex omen` | Run a judge lint over a brief, fix list, reply, or blocker. |
| `auspex inaugurate cp2` | Check the cp2 checkpoint against recorded round state. |
| `auspex read` | Print one record and its evidence paths. |
| `auspex augury` | Print a round timeline, wall time per phase, and the omen summary. |

`auspex help` prints the same list. Each command prints its full usage on a bad
flag, so run `auspex <command> -h` to read it.

## Record a round

Run these in order as a round advances.

1. `auspex appoint --slice <bead> --round <id> --tier <tier>` records the slice.
2. `auspex observe --slice <bead> --base <rev> --head <rev> --mutant <patch> --test <path>` runs the legs.
3. `auspex heed --slice <bead> --seat <A|B|composition> --hash <rev> --model <model> <transcript>` records each oracle verdict.
4. `auspex decree --slice <bead> --kind <kind> --item <id> --ruling <text>` records each ruling.
5. `auspex inaugurate cp2 --round <id>` checks the checkpoint.
6. `auspex augury <round-id>` prints the timeline.

`--tier` is `light`, `standard`, or `heavy`. `--kind` is `scope`, `triage`,
`deviation`, or `contract`. `--seat` is `A` or `composition` for `heed`, and `A`
or `B` for a `triage` `decree`.

Run `auspex appoint` first. `auspex observe` and `auspex decree` refuse a bead
that was never appointed.

### Read a round back

```bash
auspex read <record-id>
auspex augury <round-id>
```

`auspex read` takes the id that every recording command prints. `auspex augury`
takes a round id and prints a timestamped timeline, so wall time per phase is a
query rather than a task.

### Run a judge lint

Set `LLMKIT_TYPESAFE_API_KEY` first. `auspex omen` exits 1 when the key is unset
or when the judge call fails, so a lint never passes by default.

```bash
auspex omen <brief|fixlist|reply|blocker> --slice <bead> <file|->
```

Pass `-` to read the document from standard input. The lint only flags problems.
It never blocks a round.

## The leg config

`auspex observe` reads `.agents/auspex.toml` from the working tree. The file
gives the full-suite command, the per-module commands, the executed-test count
regex, and a timeout for every command.

```toml
count_regex = '(?m)^(\d+)$'

[suite]
argv = ["bash", "-o", "pipefail", "-c", "go test -v ./... 2>&1 | grep -c '^--- PASS'"]
timeout = "5m"

[[module]]
prefix = "internal/calc"
argv = ["bash", "-o", "pipefail", "-c", "go test -v ./internal/calc/ 2>&1 | grep -c '^--- PASS'"]
timeout = "5m"
```

Three rules govern the file.

- `count_regex` must have a capture group. auspex sums every numeric capture
  group over every match, so use `(?m)` for per-line anchors.
- Every command needs a positive `timeout`. A command with no timeout is a
  config-load error, not a default.
- The file needs at least one `[[module]]` entry. A `prefix` matches the
  **repo-relative test path**, not a language module name. The longest match by
  whole path components wins.

Each command must both print a countable number and preserve the exit status of
the test run. The `bash -o pipefail` in the example does the second part. A
command that ends in `grep -c` without `pipefail` always exits 0, and every leg
then reads as green.

## Seeded faults

`auspex observe` proves that a new test catches a known fault. Give it a `base`
revision without the new test, a `head` revision with it, and a mutant patch that
breaks the new behavior. The named test must exist at `base`.

The three legs check three facts:

- **Leg (a)** runs the mutant at `head` with the test paths restored from `base`.
  Expected green, because no test at `base` covers the new behavior.
- **Leg (b)** runs the mutant at `head` in the module of the named test. Expected
  red with a test count, which proves the new test catches the mutant.
- **Leg (c)** runs `head` with no mutant. Expected green with a count above leg
  (a), which proves the new test is collected.

```bash
$ auspex observe --slice tmp_WcnubVTJdv-1xa \
    --base base --head head \
    --mutant m.patch --test internal/calc/calc_test.go
tmp_A3qp0o9tOZ-oct/3326b60897db
$ echo $?
0
```

`observe` prints one record id per pair and exits nonzero if any leg has the
wrong result or the sandbox killed it.

## Records

auspex stores each record as a comment on a bead, prefixed `auspex:v1`. The beads
database is the only store, so records travel with the repository through
`bd dolt push` and `bd dolt pull`.

A record id has the form `<bead-id>/<hash>`, such as
`tmp_sXR8dPk0FN-bh2/6fa4fa591d49`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The gate passed. |
| 1 | The gate failed, or an error occurred on a gate path. |
| 2 | Usage error. The usage text goes to standard error. |

A failed gate prints `obnuntiatio` with each finding on an indented line. A passed
gate prints `auspicia prospera`.

## Sandbox

`auspex observe` runs each leg in a fresh copy of the repository, so a leg never
touches your worktree. The `HostExec` backend gives workspace isolation only: a
leg inherits the host environment and the network. Every leg record names its
backend, so a reader knows the isolation level.

## Dependencies

auspex imports two packages from
[`llmkit`](https://github.com/dpoage/llmkit), pinned to an exact version in
`go.mod`:

- `sandbox` runs each leg in a fresh workspace copy.
- `decide` asks the judge-lint questions and returns calibrated probabilities.

auspex makes no chat calls and runs no agent loop.

`llmkit` is pre-1.0, and its minor versions break compatibility. The pinned
`go.mod` version is the supported one.

## License

auspex is licensed under the GNU Affero General Public License v3.0. See
[LICENSE](LICENSE) for the full text.
