# mindskein

A Go CLI that answers the morning question: what are my priorities, what is
running elsewhere, where did we leave off.

## Where the spec lives

**Not in this repo.** The product spec, roadmap, scope and decision log live in
the vault, in the notes named `MindSkein` and `MindSkein - MVP Roadmap`. Read
them before changing behaviour, and record decisions there rather than here.

This repo holds what dies with the code: implementation, tests, CI.

## Comments

**Inline comments are minimal, and only where genuinely necessary.**

- **Never reference the planning system in code** — no task or unit numbers, no
  phase names, no "implemented by …", no roadmap milestones. That belongs in the
  PR description and the vault. Code outlives the plan, and a comment naming a
  unit is stale the moment the plan moves on.
- **No status or placeholder comments.** If something is not built, do not
  describe who will build it. An empty package needs a sentence on what it is
  for, not a schedule.
- **Prefer a name over a comment.** Delete any comment that restates the code.
- **Comment only what the code cannot say**: a non-obvious constraint, a
  measured trade-off, or a correctness trap someone would otherwise reintroduce.
  Those are worth a sentence, and worth keeping accurate.
- Exported identifiers get a doc comment when the name alone is not enough.

## Verify against reality, not memory

Claude Code's on-disk formats are the input to most of this code, and several
plausible assumptions about them have already proved false. Before relying on
the shape of a transcript, a hook payload, or a git layout, check it against
real files under `~/.claude/projects/` and real repositories — then write a test
that pins the behaviour.

## Conventions

- Standard library first. A dependency needs a reason.
- Hooks must never fail the session they observe: log and swallow at the CLI
  boundary. A non-zero exit from `PreToolUse` blocks the tool call outright.
- `PreToolUse` runs on every tool call — nothing expensive belongs in it.
- Stores write atomically, temp file then rename.
- Untrusted input from hook payloads is validated before it reaches a filename.

## Commands

```
go build ./... && go vet ./... && go test ./...
gofmt -l .
```

CI runs the same three against the version in `go.mod`, which is the minimum
supported Go, not the local toolchain.
