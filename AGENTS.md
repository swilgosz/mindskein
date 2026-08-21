# mindskein

A Go CLI that answers the morning question: what are my priorities, what is
running elsewhere, where did we leave off.

## Where the spec lives

Product & Task specifications live outside of this repo, in the PKM vault, in the notes named `MindSkein` and `MindSkein - MVP Roadmap`. Read them before changing behaviour, and record decisions there rather than here.

A code-workspace has product & project folders included.

This repo holds what dies with the code: implementation, tests, CI.

## Comments

**Inline comments are minimal, and only where absolutely necessary.**

- **Never reference the planning system in code** — no task or unit numbers, no phase names, no "implemented by …", no roadmap milestones. That belongs in the PR description and the vault.
- **No status or placeholder comments.** If something is not built, do not describe who will build it. An empty package needs a sentence on what it is for only.
- **Prefer a name over a comment.** Delete any comment that restates the code.
- **Comment only what the code cannot say**: a non-obvious constraint, a measured trade-off, or a correctness trap someone would otherwise reintroduce. Those are worth a sentence, and worth keeping accurate.
- **Usage & Public interface** - Exported identifiers get a documentation page.

## Verify against reality, not memory

Before relying on the shape of a memory transcript, a hook payload, or a git layout, check it against real files under `~/.claude/projects/` and real repositories — then write a test
that pins the behaviour.

## Conventions

- Standard library first. A dependency needs a reason.
- Hooks must never fail the session they observe: log and swallow at the CLI boundary. A non-zero exit from `PreToolUse` blocks the tool call outright.
- `PreToolUse` runs on every tool call — nothing expensive belongs in it.
- Stores write atomically, temp file then rename.
- Untrusted input from hook payloads is validated before it reaches a filename.

## Commands

```
go build ./... && go vet ./... && go test ./...
gofmt -l .
scripts/scenarios.sh      # behaviour still specified but not implemented
scripts/cover.sh 85       # total coverage with -coverpkg, fails below 85%
```

CI runs the same three against the version in `go.mod`, which is the minimum
supported Go, not the local toolchain.

## Tests come from the spec, and come first

The failure mode this guards against: tests written *after* the code describe what the code does, not what it should do. Both then encode the same wrong assumption, and a green suite proves nothing. 

So:

1. **Derive scenarios from the vault spec**, before writing the implementation. The definition of done in the roadmap is the list; each bullet becomes at least one scenario, named as a sentence about behaviour.
2. **Scaffold them as failing scenarios first**, in `<pkg>_scenarios_test.go`:

   ```go
   func pending(t *testing.T, behaviour string) {
       t.Helper()
       t.Fatalf("PENDING: %s", behaviour)
   }

   func TestPriorities(t *testing.T) {
       t.Run("resolves a wikilink to its display label", func(t *testing.T) {
           pending(t, "resolves a wikilink to its display label")
       })
   }
   ```

   Never `t.Skip` for this. A skipped test reports green for work not done.
3. **Implement until green.** `scripts/scenarios.sh` prints what remains;
   `go test ./...` is the gate, and a branch is not finished while it is red.
4. **Verify the assertion, not just the line.** Coverage says a line ran, not
   that the check was meaningful: the worst bug found here sat in a function at
   100% coverage. When an assertion matters, confirm it fails against the wrong
   implementation before trusting it.
