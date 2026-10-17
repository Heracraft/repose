# Release records

One file per release, named after it (`r20261017-1.md`), holding what does
not fit on that release's line in `docs/workstreams/STATUS.md`: each branch
with the decision ids it added and what it shipped as, the base and main
commits, and what each `--live` check printed or why it did not run.

`ops/dev/release-queue record <release>` prints both the line and this
file's body from the queue's own state, so neither is retyped. The line in
`STATUS.md` points here; five release lines written by hand reached 2,142
to 3,989 characters in a file whose header asks for 600 (DECISIONS I-760).
