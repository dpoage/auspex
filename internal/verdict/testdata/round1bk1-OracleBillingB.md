{
  "verdict": "REJECT",
  "coverage": "10 families, 44 probes, 0 skipped — local://oracle-billing-B.md",
  "model": "anthropic/claude-opus-5-5 (harness role @ORACLE)",
  "blocking": [
    {
      "location": "server/src/woodshed_server/meta_db.gleam:673-675",
      "finding": "The trial_ends_at doc comment says it returns None on subscriptions and lapses. A scratch probe against the built head (/tmp/1bk1-billing-B/mut) returned: ensure_trial(at 1000) then MarkSubscribed -> Subscribed(Some(2593000)); then MarkLapsed -> Lapsed(Some(2593000)); a trial expired through effective_billing -> Lapsed(Some(2593000)). The body at :676-680 only branches on trial_started_at.",
      "explanation_inference": "The code matches seam S3 ('Some(start + trial_duration_s) for a row with a trial start'), but W1-D builds account.gleam on this contract. A caller who trusts the doc and skips the plan check would show a trial end date on subscribed and lapsed accounts, with no error. Today billing.account_view:58-61 filters by plan, so nothing is wrong on screen yet.",
      "required_fix": "Make the doc match the seam: it returns Some(start + duration) whenever the row has a trial start, whatever the plan, and callers that show it must check plan == Trial themselves. Do not change the behaviour; S3 pins it."
    }
  ],
  "nits": [
    "The reply's per-file deleted-line counts are off: numstat gives billing 131, meta_db 71 and router_billing_test 36; the reply says 129, 69 and 35. The total of 267 is right.",
    "No test pins the 30-day trial length any more. At head, with trial_duration_s set to 1_209_600, meta_db_test is 23/23 and router_billing_test 30/30 green; at base meta_db_test goes red. The brief required this, so it is a cost, not a defect.",
    "The preserve test effective_plan_walks_trial_to_lapsed_and_persists_test lost its exact-boundary assertions. They moved into trial_ends_at_is_the_first_lapsed_instant_test and the preserve test now checks ends+1. Overall coverage is the same.",
    "The retire test never asserts billing_state is gone right after retire_tenant. A mutant that drops that delete from purge_tenant_on is only caught by the older purge_tenant_removes_billing_and_keys_test, through the shared helper.",
    "KeyRejected also covers a failed key lookup, so a DB fault reads as an auth rejection. The seam pins this.",
    "The B8 blast-radius statement is a stretch: generation, bump_generation and purge_tenant were refactored into connection-level _on helpers. That is needed to avoid calling bare inside bare and to avoid copying the SQL. purge_tenant is now one owner message instead of two.",
    "The new comment in apply_subscribed_no_id ('absent stays absent') hides that a row missing at that point gets re-created by the upsert. Base did the same through the synthetic record, so this predates the slice."
  ],
  "scope": [
    "authenticate_api_key and retire_tenant have no callers in src yet (grep for 'meta_db.<name>' in server/src returns 0 for both). The round plan (§1 and S2, lines 87, 95, 142) gives those callers to W1-D. web.gleam still has the key prefix (:40) and the SHA-256 lookup (:746-747) until W1-D migrates it, so the 'Hides' claim in the 1bk.7.6 design record is only true after that slice lands."
  ],
  "verified": [
    "Incidents replayed against both builds; each fails at base and passes at head. PER-5: base shows Some(0) and \"current_period_end\":0, head shows None and null. EDGE-6 with the verifier's blocker -> checkout -> rename -> release ordering: base drops the customer 10/10 times (one provider call), head keeps customer=cus_old 10/10. EDGE-7 with the trial shortened to 14 days: base shows a trial end at +30 days but lapses at +14 days; head reports exactly the first lapsed instant.",
    "Criteria B1-B8: reproduced all three legs of each named mutant myself as targeted eunit runs; they match the implementer's counts. Extra mutants were also killed: storing a None period end as 0, restoring the two-read checkout (red 4/4 under load), adding 1 to the account view's trial end, an auth-side prefix change, purging between the reads and the bump, and bumping with None.",
    "Old data and rollback: a meta.db written by the base binary (''/0 on disk) is rewritten to NULL when head opens it, and reopening changes nothing further. Rows mixing real values and sentinels keep their real values. The base binary reads the rewritten file correctly.",
    "retire_tenant: 50 concurrent retires on one tenant give 50 successes with previous generations 0..49 in order. A failed purge still returns Ok with the bump landed. A failed read returns Error with nothing bumped and the keys intact.",
    "Seams and scope: S2 and S3 match the plan verbatim. Keeping schema_version at 2 and using a non-transactional bare thunk were both approved in the plan (R5/PB5, S2). Only the five owned files changed.",
    "Build and suite: cold build with --warnings-as-errors passes; gleam format --check passes on the five touched files; full gleam test gives 679 passed, no failures.",
    "Cleanup: scratch dir /tmp/1bk1-billing-B deleted; no worktrees or branches created; the slice worktree is clean."
  ],
  "matrix": "local://oracle-billing-B.md"
}