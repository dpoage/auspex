{
  "verdict": "VERDICT: APPROVE",
  "coverage": "Coverage: 21 families, 64 probes, 0 skipped — local://oracle-composition.md",
  "binds": "c9804fb (feat/round-1bk-1-edge; base 929d84e)",
  "model": "anthropic/claude-opus-5-5",
  "matrix": "local://oracle-composition.md",
  "blocking": [],
  "arbiter_conditions": {
    "i_authenticate_api_key": "web.gleam:800 (api_key_auth). It ran on a real boot: a minted key answered 200 on GET /api/today.",
    "i_retire_tenant": "api/account.gleam:76, inside the retire thunk passed to tenant_registry.delete_tenant at :72. It ran on a real boot: DELETE /api/account returned 204, tenant_generations holds dev|1, and api_keys is 0.",
    "ii_hides_claim": "Holds. There is no \"gt_\" literal in server/src outside meta_db.gleam (grep exit 1); the prefix constant is at meta_db.gleam:856. The sha256 hits outside meta_db are the CSP constants (web.gleam:270-282), the content hash (compiler_hash.gleam:15), the Stripe HMAC, and the RSA verify. None of them hashes a token. sha256_hex (:1462) and lookup_api_key_by_hash (:818) are private.",
    "other_callers": "delete_tenant: account.gleam:72. effective_billing: billing.gleam:46, 681. trial_ends_at: billing.gleam:47. store_error_response: 12 api sites (today.gleam:47 … assess.gleam:64). invariant_error: today:46, onboarding:26, drills:25, assess:63.",
    "greps": "All zero: web.gleam gt_/sha256_hex/lookup; handle_request_for_context in src+test; meta_db: Option in src+test; Error(_) -> Error(NotAuthenticated); Store(store_types.NotFound) -> in all 10 api files; the D2, D3, B7 and E3 greps."
  },
  "nits": [
    "N1 (edge; carried from edge-B r1 N2): web.gleam:584 holds a private copy of the 500 {\"error\":\"internal error\"} builder beside handlers.gleam:58. It is forced because handlers imports web. api/account.gleam also still has 6 inline copies (:185, :226, :301, :347, :401, :426) next to 2 new handlers.internal_error() calls, so the one file uses two conventions.",
    "N2 (edge; S1 seam cost): tenant_retire_failed carries no cause. The retire seam fn(String) -> Result(Retired, Nil) drops the sqlight error. On a real boot, a trigger that aborts tenant_generations writes followed by DELETE /api/account logs only `level=error msg=tenant_retire_failed tenant=dev`.",
    "N3 polish, stale docs (registry/billing files, made stale by edge): tenant_db.gleam:113 names meta_db.bump_generation, which is now the private bump_generation_on. tenant_db.gleam:116-117 says account computes the unlink path; the callers are now registry:717 and remove_files:158. tenant_db.gleam:150-151 says 'the sequence api/account.gleam inlines today'.",
    "N4 polish (billing file): the error text at meta_db.gleam:1123 still reads 'bump_generation returned no generation'. The KeyRejected doc at meta_db.gleam:862-864 omits the retire-purged case, which a real boot shows: a key that worked before DELETE /api/account answers 401 after it.",
    "N5 polish (errors files): progression.gleam:16-20 says every caller passes a module id 'taken from the same content'; this is false for library.gleam:382-389, which fetches the curriculum a second time. The raw_curriculum doc at packs.gleam:99-102 omits its log_drill miss-path use (today.gleam:1176). The singleton list at handlers.gleam:67-68 omits rewind_state, which migrations/0005:53 seeds.",
    "N6 polish (new, cross-wave drift): tenant_registry.gleam:222 (Evicted doc) and :478-479 (evict doc) give DELETE /api/account as evict's caller, but evict has 0 production callers now. billing.gleam:20-22 (written by W0-B while meta_db was still Option) says the handler 'answers billing: null itself before a store exists'. meta.db is now required, and null keys on services.billing.",
    "N7 (records): the design field on bead 1bk.7.6 still lists 3 KeyAuth variants and says sha256_hex/lookup 'stay pub this round'. The KeyLookupFailed revision and the privatization are recorded only in comments; the Hides claim itself holds.",
    "N8 (edge tests; carried from edge-B r1 N4): mutating web.gleam:754 to `Deleting -> Error(NotAuthenticated)` leaves router_auth, tenant_generation, handlers, hardening, api_key_policy, export, router_billing, tenant_capacity, test_services and tenant_registry all green (132 tests). The binary itself is correct: the race probe got 683 GET 503s."
  ],
  "scope": [
    "Carried, already filed as 1bk.7.15: tenant_registry.evict is pub with 0 production callers after the composition (account used it at base). The only remaining callers are tests: 12 references, including the test_services defer."
  ],
  "follow_up_bead_candidates": [
    {
      "title": "web.resolve_owner: Deleting/Evicted/StartFailed -> 401 (and UnlinkFailed -> 204) mutants survive the suite",
      "repro": "In a copy of c9804fb, change web.gleam:754 to `Error(tenant_registry.Deleting) -> Error(NotAuthenticated)`, run gleam build, then eunit woodshed_server@{router_auth,tenant_generation,handlers,hardening,api_key_policy,export,router_billing,tenant_capacity,test_services,tenant_registry}_test. Every module stays green. Expected: at least one test goes red, because a 401 signs the user out.",
      "parent": "guitartime-1bk.7"
    },
    {
      "title": "tenant_retire_failed error line carries no cause (retire seam drops sqlight.Error)",
      "repro": "Dev boot. Run `sqlite3 meta.db \"create trigger f before insert on tenant_generations begin select raise(abort,'x'); end;\"`, then DELETE /api/account. Observed: 500 plus `level=error msg=tenant_retire_failed tenant=dev` with no error field. Expected: the line names the cause.",
      "parent": "guitartime-1bk.7"
    },
    {
      "title": "Append to 1bk.7.16 (measured frequency): GET racing its own tenant's deletion -> 500 unhandled_error",
      "repro": "200 trials of 1 DELETE /api/account plus 4 GET /api/settings through router.handle_request, rotating 3 spawn orders. c9804fb: 52/800 GET 500 (48 ProcessDown Normal, 4 Noproc), 683×503, 0/200 stale-generation hits. 929d84e: 63/800 GET 500, 459×401, 68/200 hits. Under GET-first interleavings this is not a regression.",
      "parent": "guitartime-1bk.7"
    }
  ],
  "premises": {
    "gate_bites": "Planted break: 700 passed, 1 failure, EXIT=1. Unused import: --warnings-as-errors EXIT=1, reproduced twice.",
    "sentinel_rewrite": "A meta.db written by 929d84e holds '' and 0 sentinels. The first tip open turns them into NULL. The second and third opens leave the billing rows byte-identical; only schema_version.applied_at is re-stamped, which base also does. The real row t3 is untouched. schema_version stays 2 (1 row).",
    "rollback": "The 929d84e binary opens the rewritten file: fetch_billing reads None, lookup_billing_by_customer finds the row, and account_view returns cpe None.",
    "edge2": "0/200 hits at the tip vs 68/200 at base, driven through the HTTP handler.",
    "edge3": "With the onboarding arm deleted, onboarding_test fails 8 of 8 (404/403); unmutated it passes 8 of 8.",
    "edge4": "The ghost drill answers 500 with exactly 1 invariant_violated line. Mutating :1182 to Store(NotFound) gives '404 should equal 500'.",
    "per5_wire": "On a real boot, a signed real-shaped checkout gives \"current_period_end\":null. A later subscription.updated with 1900000000 gives 1900000000.",
    "per6": "On a real boot with bricked tenant migrations, the tip answers 500 with 1 tenant_provision_failed line per request; the base answers 401 with no line.",
    "edge5": "On a real boot with the trigger fault: writes 403 session_writes_disabled, reads 200, account 500, export 200, delete 204 with the trial start preserved, and 1 entitlements_unavailable line per request."
  },
  "composed_gate": {
    "cold_build": "`gleam build --warnings-as-errors`, cold: Compiled in 59.26s, EXIT=0, 0 warnings",
    "full_test": "701 passed, no failures, EXIT=0, 9m52s, supervised (689 + 8 + 4)",
    "hand_smoke": "Real dev boot. GET /api/today 200. POST /api/flags 201 {\"id\":1}. Minted key: 200 on today and flags. gt_bogus 401. DELETE /api/account 204: dev.db* removed, generation 1, api_keys 0. The key after delete 401. GET /api/today 200 on dev.g1.db, with flags []. GET /api/account 200 billing:null. The bricked-migrations boot answers 500 {\"error\":\"internal error\"}. No error lines and no token material in any log."
  },
  "glue": "0 LOC. c9804fb^{tree} equals merge-tree (9908cfa, 2e5b025, 6d70adf) = c25ba03. The first-parent delta md5 matches the slice delta for all 5 merges, and no file is touched by two slices.",
  "isolation": "Every command ran as `env -i HOME PATH TMPDIR=/tmp/1bk1-composition/tmp nix develop ..`. Scratch trees were git-archive snapshots of c9804fb and 929d84e. The hex cache was warm; the first-party build was cold. No eval kernel was used.",
  "disclosures": [
    "While checking glue I ran `git commit-tree`, which wrote one unreferenced commit object, 760e2c8 (message 'x'), plus merge-tree tree objects into the main repo's object store. No ref, index or worktree changed. gc will prune them; I did not prune.",
    "nix develop copied the scratch flake trees into the nix store; nix garbage collection can remove them.",
    "The ' M' entries on .beads/interactions.jsonl and .omp/config.yml in the main checkout predate this session (00:39 and 20:36). They are not mine."
  ],
  "cleanup": "/tmp/1bk1-composition is deleted and /tmp is back to 2213 entries. The worktree list is unchanged, 1bk1-feat is clean at c9804fb, and all hub processes I started have exited.",
  "reply": "# Composition review: round 1bk-1 at c9804fb\n\nModel: anthropic/claude-opus-5-5. The verdict binds c9804fb. I found no blocking issues.\n\n**Arbiter conditions**\n- **(i)** `meta_db.authenticate_api_key` is called at web.gleam:800, and `meta_db.retire_tenant` at api/account.gleam:76. Both ran on a real boot.\n- **(ii)** The 1bk.7.6 Hides claim holds. No file in server/src outside meta_db.gleam contains the `gt_` literal or hashes a token with sha256.\n- **Other callers and greps:** every function named in the brief has a production caller, and every grep the brief names is 0.\n\n**Seams and boundaries**\n- S1-S5 are implemented verbatim, including the ruled `KeyLookupFailed` variant, and no consumer widens them.\n- Every boundary in the module map landed. No new src module exists.\n- Glue is 0 LOC: the merged tree matches `merge-tree` exactly.\n\n**Premises:** every premortem premise holds, and each one gives a different result on 929d84e.\n\n**Composed gate**\n- Cold build with `--warnings-as-errors`: green.\n- One full `gleam test`: 701 passed, no failures.\n- Hand smoke on a real boot: every step gave the expected result (details in the yield).\n\nNits N1-N8 (including the stale-doc list for the polish pass), 1 carried SCOPE item and 3 follow-up bead candidates are in the yield data.\n\nCoverage: 21 families, 64 probes, 0 skipped — local://oracle-composition.md\nVERDICT: APPROVE"
}