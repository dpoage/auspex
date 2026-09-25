# auspex: design brief

auspex is a small CLI that runs the mechanical checks of an oracle-gated development
round. The round process is defined by the `oracle-rounds` and `arbiter-architect`
skills in nixos-config. auspex runs every check that has a deterministic answer. The
agents keep every check that needs judgment.

The name comes from the Roman auspex, who read the signs before any undertaking
began. auspex reads the signs at each gate of a round.

## Problem

The skills describe mechanical checks in prose, and model agents execute them. This
has three costs:

1. A strong model spends tokens and wall time on hash comparisons, label checks, and
   command sequences.
2. Agents misreport results. In one measured round, 22 of 31 fix dispatches were
   followed by a collateral edit or a false claim. The skills answer this by having a
   second agent measure the first. A script measures more cheaply and cannot invent
   output.
3. Every mechanical rule in prose is context that each agent must hold and can apply
   wrongly.

## Goals for v1

v1 replaces the checks with the highest cost and the lowest reliability:

- **Mutant legs.** `auspex observe` runs the three legs of every mutant, stores the
  raw transcripts, and records the result. Implementers cite a record instead of
  pasting transcripts. Seat B reads the record and re-runs a sample.
- **CP2 ledger check.** `auspex inaugurate cp2` evaluates the CP2 conditions against
  recorded state. It exits 0 or lists each failed condition. The arbiter reads the
  output and spends its attention on rulings and the random sample.
- **Judge lints.** `auspex omen` asks the `oracle-rounds` judge-lint questions through
  TypeSafe Jev, applies the skill's thresholds, and records each result. The lints
  still only flag. auspex makes sure they run every time and keeps the results.

`auspex heed` and `auspex decree` are in v1 because `inaugurate cp2` needs recorded
verdicts and rulings.

Every record carries a timestamp. Wall time per phase is then a query, not a task.

## Dependencies

auspex is written in Go and imports two packages from llmkit
(`github.com/dpoage/llmkit`):

- **`sandbox`** runs every leg. `Exec` copies `RepoDir` into a fresh workspace per
  call, so a leg never touches the implementer's worktree. `Result.InfraKilled`
  separates a sandbox kill from a test result. A timeout or out-of-memory kill never
  counts as the red of leg (b). `sandbox.Mock` gives the legs module hermetic unit
  tests.
- **`decide`** asks the judge-lint questions. It returns calibrated probabilities and
  choice confidences, which the skill's thresholds apply to directly.

auspex uses no other llmkit package. auspex makes no chat calls and runs no agent
loop: judgment stays with the agents.

auspex is AGPL-3.0, the license of llmkit. As a local CLI, it has no network users, so
the AGPL terms work like the GPL here. llmkit is pre-1.0, and its minor versions break
compatibility. `go.mod` pins an exact llmkit version.

### Sandbox backend

v1 starts on the `HostExec` backend. Bwrap is a spike with an exit.

- **`HostExec`** runs each leg as a host process in a fresh copy of the repository.
  It gives workspace isolation only: the leg inherits the host environment and the
  network. Every leg record states the backend, so a reader knows the isolation.
- **Bwrap** adds no network, a sized tmpfs, and resource caps. That is the sterile
  run the premortem's evidence family asks for. The risk is Nix: the suite runs inside
  `nix develop`, and Bwrap with no network must reach the Nix store and a resolved
  environment. The spike resolves the environment outside the sandbox
  (`nix print-dev-env`), passes it through `Spec.Env`, and mounts `/nix/store`
  read-only. If the spike does not pass acceptance item 1 on guitartime within one
  session, v1 ships on `HostExec` and Bwrap waits for the outcome ledger to show a
  need.

The backend is one config value. The legs module never names a backend.

## Non-goals

- Scheduling or spawning agents. omp does this.
- A review UI.
- A second store for round state. Round state lives in beads.
- Tamper resistance. The failure mode is false claims, not an adversary. Stored
  transcripts carry a SHA-256 hash, and the checks re-verify it.
- Any file committed to a product repository besides one declarative config file
  (open question 2). The skills forbid committed harnesses in product repositories.

## Command surface (v1)

The command names follow Roman augury. Help text, error bodies, and record fields use
plain words. Each command has one name and no aliases.

```
auspex observe    --slice <bead> --base <rev> --head <rev> --mutant <patch> --test <path>... [--mutant ... --test ...]
auspex appoint    --slice <bead> --round <round-id> --tier <light|standard|heavy> [--merge-hash <rev>]
auspex heed       --slice <bead> --seat <A|B|composition> --hash <rev> --model <model> [--local <dir>] <transcript-file|->
auspex decree     --slice <bead> --kind <scope|triage|deviation|contract> --item <id> --ruling <text> [--seat <A|B>] [--to <bead>...]
auspex omen       <brief|fixlist|reply|blocker> --slice <bead> <file|->
auspex inaugurate cp2 --round <round-id>
auspex read       <record-id>
auspex augury     <round-id>
```

| Command | Roman source | What it does |
|---|---|---|
| `observe` | *servare de caelo*: the augur watched the sky for signs | Runs the mutant legs. |
| `appoint` | *Inauguratio* confirmed an appointment before it took effect | Records a slice's tier and the hash it will merge. |
| `heed` | Heeding the oracle's pronouncement | Records an oracle verdict. |
| `decree` | The *decreta* of the college of augurs | Records a ruling. |
| `omen` | *Auspicia oblativa*: signs that appeared unasked. The magistrate could heed them or set them aside | Runs the judge lints. A flag is advice and never halts a round. |
| `inaugurate` | *Inauguratio*: the augur confirmed the gods' approval before an appointment took effect | Checks a checkpoint. |
| `read` | Reading the recorded signs | Prints one record. |
| `augury` | The whole reading | Prints a round's timeline and wall time per phase. |

Outcome headlines are themed; the lines after them are plain:

```
auspicia prospera: cp2 holds for round 1bk-1 (5 slices, 14 records)

obnuntiatio: cp2 fails for round 1bk-1
  slice guitartime-1bk.7.4: seat B has no APPROVE bound to 3f2a1c04b9e7d15a2c68f0e3b7d4a9c1e5f28b60
  slice guitartime-1bk.6.4: SCOPE item guitartime-1bk.6.4/9bd5cbad0f8e#S2 has no ruling
```

*Obnuntiatio* was an augur's announcement of an unfavorable sign. It legally halted
public business, which is what a failed gate does.

- **`observe`** takes one or more mutant/test pairs. The legs are:
  - (a) At `head`, apply the mutant and restore the test paths from `base`. Run the
    full suite. Expected result: green.
  - (b) At `head`, apply the mutant. Run only the module of the named test. Expected
    result: red, not `InfraKilled`, and with an executed-test count. A leg (b) that
    is red without a count (a crash, a kill, a misconfigured command) is not evidence
    that the test catches the mutant.
  - (c) At `head`, apply no mutant. Run the full suite once for all pairs. Expected
    result: green, with an executed-test count that exceeds the count from leg (a).
    A criterion must therefore add a test function; extending an existing test fails
    leg (c).

  The suite and module commands, the executed-test count regex, and a timeout per
  command come from `.agents/auspex.toml` in the working tree. The regex must have a
  capture group. A test path's module command is the entry whose path prefix matches
  it longest, by whole path components; for Gleam that is the package. A leg whose
  output the sandbox truncated has no count. Before any leg runs, `observe` checks
  that the slice is appointed, the config loads, every mutant applies, and every
  test path maps to a module. It exits nonzero when any leg has the wrong result or
  a sandbox kill, and prints one record ID per pair.
- **`appoint`** records the slice's tier, and its merge hash once known, and adds the
  label `auspex-slice:<round-id>`. Appoint each slice at slicing, and again with
  `--merge-hash` before CP2. A later `appoint` supersedes the earlier one; both stay in
  the record history. Every other command refuses a bead that was never appointed.
- **`heed`** reads the payload an oracle yields under its output schema: a JSON object
  with `verdict`, `coverage`, `matrix`, `scope` (a list of `{id, text}`) and `reply`.
  Plain text, a missing or mistyped field, a blank scope text, and a premortem `PLAN:`
  verdict are not verdicts. A `VERDICT` word anywhere else in the payload that
  disagrees with `verdict` also makes it not a verdict. `matrix` must equal the path
  in the `Coverage:` line; for an APPROVE the file must exist, resolving `local://`
  paths against `--local` and staying inside it. `heed` stores the transcript as
  evidence and records the verdict bound to `--hash`, with the seat's resolved model
  from `--model`. Every `scope` entry is an item. Item `k` has the id
  `<verdict-record-id>#S<k>`, and `heed` prints each id. Oracles restart their own
  `S1` numbering in every reply, so an oracle label cannot identify an item across
  re-reviews. An item carried into a re-review gets a new id and needs its own ruling.
- **`decree`** records a ruling on a `SCOPE:` item, a loop-cap triage, a deviation,
  or a contract revision. A triage names its seat with `--seat`. A contract ruling
  names with `--to` the slices it is addressed to: the consumers for a revision, the
  owner for an acknowledgement.
- **`omen`** asks the question for the named kind, word for word from the
  `oracle-rounds` judge-lint table, and flags at the table's threshold. The skill
  judges one paragraph or bullet at a time, so `omen` splits the text into paragraphs
  and top-level bullets, judges each, and flags when any unit flags. Skill routing
  judges the whole brief against every skill under `~/.agents/skills` and the
  repository's `.agents/skills`. `omen blocker` records a PRODUCT, TEST_MACHINERY, or
  PROSE class per blocker. A class with confidence under 0.8 is recorded as
  unclassified and printed as needing the orchestrator's class. For every lint, the
  recorded `p` is the value compared with the threshold; for a blocker it is the
  class confidence. `augury` reports the non-product share of classified blockers per
  round, which is the skill's Overscope signal, and counts unclassified ones apart.
- **`inaugurate cp2`** evaluates these conditions for every bead with the label
  `auspex-slice:<round-id>`. Its headline names the slice count, so the arbiter can
  compare it with the slice map:
  - The round has at least one slice, and every slice has an appointment with a merge
    hash. A round with no slices fails.
  - For every seat that the slice's tier names, the seat's latest verdict bound to the
    merge hash is an APPROVE.
  - Every REJECT ends in an APPROVE from the same seat.
  - Every `SCOPE:` item has a ruling whose item is its full id,
    `<verdict-record-id>#S<k>`.
  - Every even-numbered consecutive REJECT from one seat (2nd, 4th, …) has a triage
    ruling for that seat, recorded strictly between that REJECT and the seat's next
    verdict. Each triage buys one fix round.
  - Every contract revision reached every slice it names. Rulings on an item are read
    in time order: a ruling is an acknowledgement when it names exactly one other
    slice, and an earlier revision of the item on that slice names it back. Every
    other contract ruling is a revision, and each slice it names must record a ruling
    on the item at or after it. The first ruling on an item defines its owner, so an
    acknowledgement recorded before any revision reads as the revision.
  - A slice with leg records has at least one bound to its merge hash, and every leg
    record at the merge hash has the correct results.
  - Every leg and verdict record has its evidence refs, and every stored file still
    matches its hash.

  cp2 reads only a slice's records from its first appointment in the queried round
  onward, and ignores composition verdicts. A slice whose criteria take no legs
  (deletion and type-shape criteria) passes the leg condition with no leg records.
  A slice with no merge hash is reported once, by the appointment condition.
- **`read`** prints a record, each evidence path by role, and exits nonzero when a
  referenced evidence file is missing, corrupt, or was never stored.
- **`augury`** prints every record of a round in time order, the wall time per phase,
  and the omen summary. A flag prints as `omen: <lint> flagged at <p> (threshold <t>)`,
  an unclassified blocker as `omen: blocker unclassified at <p> (threshold 0.8)`.
  Phases come from records only: first appointment to first leg record, first leg record to
  each seat's first verdict, each REJECT to that seat's next verdict (one fix round),
  and the composition verdict. Premortem and polish leave no record and are not shown.

## Modules

Each module has a five-part record, as the `module-design` skill defines it. Go
package names are plain and name the decision each package hides. The augury
vocabulary stays on the command surface.

### evidence

1. **Hides.** Callers do not know where transcripts live, how they are named, or how
   their integrity is checked. Transcripts are content-addressed files under
   `$XDG_STATE_HOME/auspex/evidence/`, named by SHA-256. One store serves every
   repository: a content address cannot collide, and a per-repository key would have to
   be stable across clones, which the root-commit candidate is not (bd's dolt sync
   rewrites a remote ref with a new root commit).
2. **Interface.** `put(bytes) -> Ref`; `open(Ref) -> bytes | Corrupt`.
3. **Callers.** `observe` and `heed`.
4. **Rewrite cost.** The evidence module and its tests.
5. **Deletion cost.** `observe` and `heed` each choose paths, write files, and hash
   them. The integrity rule would then live in two places. Keep the module.

### ledger

1. **Hides.** Callers do not know that round state is stored as bead comments. They
   also do not know the comment prefix, the record encoding, or the `bd --json`
   invocations.
2. **Interface.** `append(slice, Record)`; `round(round_id) -> [Slice]`, where a
   `Slice` has a tier, a merge hash, and its records in time order. A `Record` is one
   of `Appointment`, `Leg`, `Verdict`, `Ruling`, or `Lint`, each with a timestamp. A
   slice's tier and merge hash come from its latest `Appointment`.
3. **Callers.** Every command.
4. **Rewrite cost.** The ledger module and its tests. A move off beads changes only
   this module.
5. **Deletion cost.** Every command constructs `bd` arguments and parses `bd` JSON.
   The metadata schema would be spread across five commands. Keep the module.

### legs

1. **Hides.** Callers do not know the leg protocol: the mutant application, the test
   restoration from `base`, the suite and module commands, the parsing of
   executed-test counts, and which sandbox backend runs them.
2. **Interface.** `run(sb sandbox.Sandbox, repo_config, base, head, [(mutant, [test_path])]) -> [LegResult]`.
3. **Callers.** The `observe` command, and `acceptance-replay` through it.
4. **Rewrite cost.** The legs module, its tests, and the repository config schema
   (open question 2). `main` constructs the sandbox backend.
5. **Deletion cost.** Implementers run the legs by hand, which is the status quo this
   tool exists to replace.

### verdict

1. **Hides.** Callers do not know the oracle verdict contract: the line formats, the
   matrix path convention, and the rule that an APPROVE without a matrix is not a
   verdict.
2. **Interface.** `parse(transcript) -> Verdict | NotAVerdict(reason)`.
3. **Callers.** The `heed` command. The oracle's own def remains the specification.
   A test pins the parser against one real transcript from each seat type.
4. **Rewrite cost.** The verdict module and its tests.
5. **Deletion cost.** The `heed` command parses inline. Keep it separate only if
   `inaugurate` also needs to re-parse transcripts. Decide during implementation.

### check

1. **Hides.** Callers do not know the checkpoint predicates or how a failure maps to
   a message.
2. **Interface.** `cp2([Slice]) -> [Failure]`.
3. **Callers.** The `inaugurate` command.
4. **Rewrite cost.** The check module and its tests.
5. **Deletion cost.** The arbiter evaluates the predicates in prose, which is the
   status quo.

### lint

1. **Hides.** Callers do not know the judge-lint questions, their thresholds, the
   blocker classes, or that TypeSafe Jev answers them.
2. **Interface.** `ask(kind, text) -> Lint`, where a `Lint` holds the question, the
   probability or choice with its confidence, and whether it flags.
3. **Callers.** The `omen` command. `augury` reads recorded `Lint` records through the
   ledger.
4. **Rewrite cost.** The lint module and its tests. `main` constructs the `decide`
   client.
5. **Deletion cost.** The orchestrator calls `judge()` by hand, forgets some calls,
   and keeps no history. The Overscope share is never computed.

## State model (bead comments)

Every record is one bead comment on its slice bead: a fixed prefix, then the record
as one JSON object. Comments are append-only. `appoint` adds the label
`auspex-slice:<round-id>`, which marks the slice beads of a round. The skills label
every round bead `round:<round-id>`, and a slice holds several beads, so auspex keeps
its own label for slices. A record holds its kind, timestamp, seat or leg, bound hash,
result, and evidence refs. auspex writes no bead metadata.

Bead metadata was the first design. A probe rejected it: 16 concurrent
`bd update --set-metadata k<i>=<i>` calls on one bead (bd 1.2.2) all exited 0, and
only 4 keys survived (7 on a rerun). 16 concurrent `bd comments add` calls kept all 16.
Seats A and B record verdicts on one slice at the same time, and a lost REJECT would
let the triage check pass falsely.

## Open questions

1. **Jev credentials.** `omen` reads `LLMKIT_TYPESAFE_API_KEY` and fails closed when
   it is unset. The key in `~/.config/bugbot/env` (exported there as
   `LLMKIT_LIVE_TYPESAFE_API_KEY`) passed llmkit's live `decide` lane on 2026-09-23.
   Open: where nixos-config provides it under the production name.
2. **Repository config location.** Decided: `.agents/auspex.toml` in the product
   repository, next to the repository overlay skill. It holds the full-suite command,
   the per-module command template, and a regex for the executed-test count. The file
   is declarative, and CI does not run it.
3. **Record size.** Decided: records are bead comments (see State model). A 5 MB
   metadata value also round-tripped intact, so size was never the constraint;
   concurrent writes were.
4. **Rulings by the arbiter.** A ruling is a claim by an agent. `auspex decree` only
   records that a ruling exists. The CP2 random sample still judges whether the
   ruling was sound.

## Integration with the skills

After v1 is implemented and verified, the skills change in one cutover:

- The implementer and implementer-max defs replace the three-transcript rule with
  "run `auspex observe`; cite the record IDs". The defs keep their fourth leg, which
  runs the shipped artifact with nothing exported; `observe` does not cover it.
- `acceptance-replay` step 4 reads the leg records and re-runs one pair with
  `auspex observe`.
- The `oracle-rounds` judge-lint section becomes "run `auspex omen`". Its round
  lifecycle names `auspex appoint` at slicing and before CP2, `auspex heed` for every
  verdict, and `auspex decree` for every ruling; without them `inaugurate cp2` sees no
  slices.
- The `oracle` def gains a fixed output schema: `verdict`, `coverage`, `matrix`,
  `scope` as `[{id, text}]`, and `reply`. `heed` reads only that schema, so older
  transcripts must be hand-normalized first.
- The `arbiter-architect` CP2 section becomes "run `auspex inaugurate cp2`; rule on
  each failure; take the random sample". Deviation rulings have no cp2 condition and
  stay in the arbiter's prose audit.
- nixos-config packages auspex and installs it with the skills.

## Acceptance for v1

0. **Bwrap spike (time-boxed to one session).** On guitartime, run acceptance item 1
   on the Bwrap backend with the environment resolved outside the sandbox. Pass: all
   legs give their expected results with no network. Fail: record why, and v1 ships
   on `HostExec`.
1. **Legs discriminate.** On guitartime, take round-1 criterion E2: base 9908cfa,
   head 6d70adf, the mutant at `server/src/woodshed_server/service/progression.gleam:158`,
   and the test in `server/test/woodshed_server/service/today_test.gleam`.
   `auspex observe` reports (a) green, (b) red, and (c) green with a higher
   executed-test count. Then remove the new test from `head`: leg (b) goes green, and
   `observe` exits nonzero.
2. **Legs are isolated.** After any `observe` run, `git worktree list` and
   `git status --short` in the implementer's worktree are unchanged.
3. **A kill is not a red.** Set a leg timeout shorter than the test module. Leg (b)
   reports `InfraKilled`, and `observe` exits nonzero instead of recording red.
4. **CP2 check agrees with a real round.** After guitartime round 1 reaches CP3,
   backfill its verdicts and rulings with `auspex heed` and `auspex decree`. The
   round-1 transcripts predate the oracle schema, so the backfill uses the
   hand-normalized copies in `internal/verdict/testdata/normalized/`.
   `auspex inaugurate cp2` then prints `auspicia prospera`. On a copy of the beads
   database without one recorded ruling (bd cannot delete a comment), it prints
   `obnuntiatio`, names the unruled item, and exits 1.
5. **Integrity.** Change one byte of a stored transcript. `inaugurate cp2` reports
   the hash mismatch.
6. **Lints match the skill.** For each kind, one known-positive and one
   known-negative text from round 1 flag and do not flag at the skill's thresholds.
7. **Wall time.** `auspex augury` on a fixture round with known record times lists
   the wall time per phase. A backfilled round carries backfill times, so real phase
   times are first measured in the shadow rounds.

## Validation: is auspex right, and does it pay?

Acceptance proves that v1 works as built. This section decides whether auspex
replaces the prose checks at all. auspex is a gate, so a broken auspex is worse than
none: a false `auspicia prospera` is as silent as a false oracle APPROVE.

### Fail closed

- Any error, unknown record shape, missing transcript, or `bd` failure prints
  `obnuntiatio` and exits nonzero. No code path reports a pass by default.
- `observe` records the backend, the commands, and the executed-test counts in every
  leg record, so a reader can see what a green meant.

### Seeded-fault corpus

Before any shadow round, build a corpus of known-bad cases from real round history:

- A test that passes under its mutant (leg (b) green).
- A new test that CI does not collect (leg (c) count unchanged).
- A leg killed by timeout.
- An APPROVE with no matrix file, and an APPROVE bound to a stale hash.
- An unruled `SCOPE:` item, and a triage missing after 2 REJECTs.
- A stored transcript with one changed byte.

auspex must catch every case. Any miss blocks the shadow rounds. The corpus lives in
the auspex repository and runs in its CI. Every false pass found later becomes a new
case.

### Review auspex like round code

auspex is Heavy by the tier table: it gates merges. Build it through an oracle round
with both seats. Seat B's acceptance replay runs the seeded-fault corpus and the v1
acceptance items against the built binary.

### Shadow rounds

Run auspex beside the prose process for two rounds. The prose process stays binding.

1. Implementers paste legs as today, and they also run `auspex observe`.
2. The arbiter does the prose CP2, and it also runs `auspex inaugurate cp2`.
3. The orchestrator runs the judge lints as today, and it also runs `auspex omen`.
4. Log every disagreement between auspex and the prose process, with which side was
   right after a probe.

### Measure against a baseline

The skill changes of 2026-09-22 (tiers, N+1 legs, the smaller CP2) land before
auspex. To tell their effect from the effect of auspex, compare three rounds:

- **Baseline A:** guitartime round 1, the old rules.
- **Baseline B:** the first round on the new rules, without auspex.
- **Shadow:** the rounds with auspex in shadow.

Record per round, from the step 11 wall-time line and the transcripts:

- The wall time from a slice's finish to its first verdict pair.
- The oracle and arbiter tokens spent on leg re-execution and CP2.
- The false claims caught: leg or state claims that failed measurement, and by
  which side.
- The auspex invocation errors made by agents. This is the caller-friction signal.

### Caller usability probe

Give a cheap agent (`sonic`) only `auspex --help` and the one skill line that names
each command. Ask it to run a leg, record a verdict, and check CP2 on a fixture
round. Each wrong invocation is a help-text or interface defect, and it gets fixed
before the shadow rounds.

### Cutover and kill criteria

Cut the skills over to auspex only if all of these hold after the shadow rounds:

- auspex has no false pass, in the corpus or in the shadow rounds.
- On every disagreement, auspex was right, or the cause is fixed and has a corpus
  case.
- auspex caught at least one false claim that the prose process missed, or it cut
  the time from finish to verdict by a margin the arbiter accepts.
- Agents invoked auspex correctly after the usability probe fixes.

If the rounds show no catch and no time saved, auspex does not cut over. Then keep
only the parts that earned a place (for example `augury` for wall time), or archive
the repository. The cutover is one skill commit plus the Nix package, so reverting
it is one revert.

## Later, if the outcome ledger justifies it

- `auspex audit <prev> <new>`: blast-radius check on a fix diff.
- `auspex tier`: tier from path and state patterns in the repository config.
- `auspex inaugurate cp1` and `auspex inaugurate cp3`.
- An `escaped-from` report over the outcome ledger.
