# Command-output optimization experiment

Branch: `optimization`, based on `7b9627d`.

The first experiment applies RTK's command-output reduction principle with
Prism's existing executor, history and file tools. The implementation is original
Go code; it introduces no RTK binary, engine, proxy or model dependency.

## Scope

- Opt-in server flag: `PRISM_COMPACT_COMMAND_OUTPUT=1`.
- Go test text and Node `--test` TAP only. One literal `cd path &&` prefix is
  accepted; pipelines, redirects, expansions and ambiguous commands pass through.
- No command rewriting, extra executions, extra model calls, tool schema changes
  or additional instructions in the system prompt.
- Complete quiet successes are reduced to explicit counters. Error messages,
  test contexts carrying diagnostics, skipped tests, package paths and unknown
  lines are preserved. Unknown formats, JSON and binary output are not filtered.
- Compare with the actual existing head/tail preview, including the recovery
  pointer and unchanged execution error. Accept only savings of at least 256
  bytes and 20%; otherwise preserve the old response.
- `rawResults` bypasses chat previewing for programmatic `exec_command`. This
  also corrects its previous unconditional stdout truncation in that path.

## Recovery and stable history

Before a reduced result is returned, stdout and the available execution error
are saved in the existing execution volume. Recovery uses `read_file` and
`exec_command`, without a new agent-facing capability. Successful stderr is not
captured by the existing Docker interface and is not claimed as recoverable.

The filename hashes an unambiguous tuple of command/stdout/error, prefixed by a
hash of the session. The command itself is not written into the file. Identical
observations produce the same response; different raw observations retain
different identities even when the displayed success summary is identical. This
preserves the distinction used by the repetition detector. Existing entries are
checked before reuse; storage failure or altered content falls back to the old
preview. The original error value is returned unchanged.

The UI, persisted message and subsequent model requests retain the same reduced
representation. Old messages are not rewritten based on their age. Disabling the
flag does not re-expand saved results. This keeps the existing replay contract
and avoids introducing a turn-dependent rewrite of the provider's cache prefix.
The complete recovery copy is a separate, bounded file cache rather than a
permanent conversation archive.

Files use the same permissions/sharing model as ordinary workspace files, not a
new cross-user isolation boundary. The cache has limits of 2 MiB per file,
32 files per session and 128 files per workspace. File operations are confined
with `os.Root`; cleanup is serialized and restricted to generated filenames.
Mandatory Git exclusions cover existing workspaces and custom `.gitignore`
files, including snapshots after a restore, so deleted recovery copies do not
remain indefinitely in automatic workspace history.

## Measurement

Content-free `command_output` usage events record stdout/error bytes, baseline
response bytes, final response bytes, filter and fallback mode. Existing provider
usage counters continue to measure the model requests independently. No tokens
or dollar amounts are inferred from byte ratios.

The offline corpus includes one captured public Prism test transcript and ten
explicitly synthetic cases. The captured command runs only local time/cron tests.
Six cases cover unknown text, JSON, source code and command mismatches. They keep
the previous response. The four large recognized cases include successful tests
and failures placed in the middle of output that the old preview omitted.

The rendered-response test uses the real executor formatter, recovery pointer,
gain guard and file cache, not an estimated pointer length:

| Case | Current stdout preview | Final response | Reduction |
| --- | ---: | ---: | ---: |
| Captured Prism tests | 1,000 B | 319 B | 68.10% |
| Synthetic Go: 240 successes | 8,019 B | 283 B | 96.47% |
| Synthetic Go: central failure | 8,019 B | 469 B | 94.15% |
| Synthetic TAP: 160 successes | 8,019 B | 392 B | 95.11% |
| Synthetic TAP: central failure | 8,019 B | 798 B | 90.05% |

Across this unweighted corpus: 81,190 B of current responses versus 50,375 B
with compaction, a 37.95% reduction. These are stdout-only byte measurements on
this corpus, not representative API savings. Assertions check that central
failure diagnostics remain visible and that recovery copies match the original
stdout. Filtering alone measured approximately 12 microseconds for the captured
transcript and 170–202 microseconds for the large synthetic cases on the test
host; these timings exclude hashing, archive I/O and provider requests.

## Validation and next milestone

Tests exercise enabled/disabled behavior, programmatic calls, error identity,
command/environment preservation, recovery through existing tools, unknown and
structured outputs, file tampering, symlinks, cache bounds, concurrent reuse,
UTF-8, stable identity and the repetition detector. A disposable PostgreSQL test
checks UI/model/archive agreement and exact replay after disabling the flag.

Completed checks:

- `go test -race ./... -count=1`, using only fake provider endpoints and a
  disposable PostgreSQL database for database-backed tests.
- `go vet ./...`.
- A focused race-enabled regression test for restoring a historical workspace
  tree containing recovery files, with exclusions applied again after checkout.
- Compose resolution with the experimental flag unset and set to `1`, without
  recreating any running application container.
- The final rendered-response corpus report. The disposable database was removed.

Temporary validation logs: `/tmp/prism-optimization-go-race.log`,
`/tmp/prism-optimization-vet.log`, `/tmp/prism-optimization-git-restore.log`
and `/tmp/prism-optimization-corpus-final.log`.

Before enabling this by default, compare end-to-end completion on representative
tasks with a small model and Claude/GPT. Count additional reads/retries and the
actual provider input/output/cache usage, including auxiliary calls. Prism's
current monetary estimates cover only selected Claude models, and chat-loop
counters exclude some auxiliary model calls. No production instance is changed
by this experiment; no paid provider benchmark has been run.
