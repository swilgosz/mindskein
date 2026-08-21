# mindskein

One command for the question you ask every morning: **what are my priorities,
what's running elsewhere, and where did we leave off?**

```
PRIORITIES
  !1  Installer rewrite       traffic lands Wednesday, needs the signed build
  !2  Onboarding email        draft exists, unsent
      Billing reconciliation  blocked on the export format

  4 more in the backlog (--all)

LIVE SESSIONS
  a3b12c9f  Installer rewrite    acme-cli    running          12m  (Edit)
  e0cc146a  Onboarding email     notes       waiting        1h04m  (idle_prompt)

  2 sessions · 1 running · 3 ended hidden (--all)

WHERE WE LEFT OFF
  acme-cli · signing   2026-08-21 23:40  the notarization step still fails on…
  notes                2026-08-21 18:12  rewrite the second paragraph, it's flat
  acme-api             2026-08-20 21:05  (no prompt recorded)
```

Three sections, three sources: a markdown file you already keep, a registry of
live Claude Code sessions, and the last handoff from each piece of work.

## What it is

A single Go binary and four Claude Code hooks. No daemon, no database, no
server, no account. The hooks record what your sessions are doing into
`~/.mindskein/`; the CLI reads it back.

It exists because the alternative is asking Claude the same four questions
every morning and stitching the answers together by hand.

## Install

**With Go** (needs Go 1.23+):

```sh
go install github.com/swilgosz/mindskein/cmd/mindskein@latest
```

**From a release:** download the archive for your platform from
[Releases](https://github.com/swilgosz/mindskein/releases), verify it against
`checksums.txt`, and put `mindskein` somewhere on your `PATH`.

## Set up

Two steps. Neither is done for you yet — see [Status](#status).

### 1. Point it at your plan

`mindskein` does not guess where your notes live, because a wrong guess prints
an empty list, which looks exactly like a quiet day. Write
`~/.mindskein/config.toml`:

```toml
[vault]
path = "~/notes"          # optional base; ~ expands
plan = "plan.md"          # relative to path, or absolute

[status]
hide_after = "7d"         # drop sessions quiet longer than this; "0" keeps all
```

A priority is a checkbox **and** a level token:

```markdown
- [ ] !1 Installer rewrite - traffic lands Wednesday
- [ ] !2 Onboarding email
- [x] !1 Fix the login bug        ← ticked, so it stops showing
```

The full contract — what splits a label from its note, how wikilinks resolve,
what is deliberately not supported — is in [`docs/plan-format.md`](docs/plan-format.md).

### 2. Register the hooks

Nothing is recorded until Claude Code calls the hooks. **Back up your settings
first** (`cp ~/.claude/settings.json ~/.claude/settings.json.bak`), then merge
this into `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      { "hooks": [{ "type": "command", "command": "mindskein hook pre-tool-use", "timeout": 5 }] }
    ],
    "Notification": [
      { "matcher": "idle_prompt|permission_prompt|agent_needs_input",
        "hooks": [{ "type": "command", "command": "mindskein hook notification", "timeout": 5 }] }
    ],
    "Stop": [
      { "hooks": [{ "type": "command", "command": "mindskein hook stop", "timeout": 5 }] }
    ],
    "SessionEnd": [
      { "matcher": "clear|resume|logout|prompt_input_exit|other",
        "hooks": [{ "type": "command", "command": "mindskein hook session-end", "timeout": 5 }] }
    ]
  }
}
```

Hooks reload without restarting Claude Code. Open a session, run a tool, then
`mindskein status` — it should be listed. If it is empty, the hook shell
probably cannot find `mindskein` on its `PATH`; use the absolute path from
`which mindskein` instead.

Keep the `timeout`. Leaving it out does not mean "no timeout" — it means the
Claude Code default, which is far longer than anything this should ever need.

## Use

```sh
mindskein brief         # all three sections — the morning read
mindskein status        # live sessions only — the mid-day check
mindskein priorities    # the plan only
```

`--all` widens every section: the `!3` backlog, sessions that have ended or
aged out, and every workstream rather than the newest few.

Set `MINDSKEIN_PROJECT` before launching Claude to name a workstream that spans
several folders and sessions. It is the only grouping key that does.

## What it stores, and where

```
~/.mindskein/
  config.toml       you write this; the app never rewrites it
  sessions/         one JSON file per session — id, folder, status, last tool
  handoffs/         one markdown file per session — title, times, last prompt
  hooks.log         appended to only when a hook fails
```

**Everything stays on your machine.** Nothing is uploaded, and the tool makes
no network calls at all.

Be aware of what a handoff holds: **the last message you typed**, verbatim, up
to 1500 characters. That is the point — it is what answers "where did we leave
off" — but it means anything you paste into a session can land in
`~/.mindskein/handoffs/`. The directory is created `0700` and the files `0600`.

To remove everything: `rm -rf ~/.mindskein` (and un-register the hooks).

## How it works

| Hook | When | What it records |
| --- | --- | --- |
| `PreToolUse` | every tool call | session is `running`, and the tool it just used |
| `Notification` | idle / permission prompts | session is `waiting` on you |
| `Stop` | every turn completion | session is between turns, **and** writes the handoff |
| `SessionEnd` | `/clear`, `/resume`, logout, exit | session `ended`, with the reason |

`PreToolUse` runs on *every* tool call, so it is kept deliberately cheap —
about 8.7 ms, and it never opens a transcript. `Stop` is the only hook that
reads the transcript, because it is the only one that fires where "where did we
leave off" has an answer.

`Stop` fires **per turn**, not once per session, so a handoff is rewritten
throughout a session and always describes the latest state.

## Status

Working and used daily. Not yet at its own v0.1 definition of done, and there
are sharp edges worth knowing before you install it:

- **A hook crash can disturb the session it is watching.** The hooks are
  registered synchronously and there is no panic guard yet, and a Go binary
  exits `2` on an unrecovered panic — which `PreToolUse` reads as *block this
  tool call*. Fixing this is the next thing on the list, and it lands before
  any release that asks other people to install it.
- **Nothing is ever deleted.** `Stop` writes a handoff every turn and no record
  is ever pruned, so `~/.mindskein/` only grows. `status` hides what is stale
  or ended; it does not remove it.
- **Setup and removal are manual.** There is no `mindskein init` and no
  uninstall, so both steps above are hand-edited files. Uninstalling the binary
  without un-registering the hooks leaves them pointing at a command that no
  longer exists.
- **A session that dies hard is still reported as running** until it ages past
  `hide_after`. No hook fires when a terminal is killed.

## Contributing

`go build ./... && go vet ./... && go test ./...`, plus `gofmt -l .` and
`scripts/cover.sh 85`. CI runs the same against the Go version in `go.mod`.

Behaviour is specified as failing scenarios before it is implemented — see
[`AGENTS.md`](AGENTS.md) for why, and for the rest of the repo conventions.

## License

[AGPL-3.0](LICENSE).
