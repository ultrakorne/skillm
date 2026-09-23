# skillm — Known Issues & Deferred Work

Confirmed defects that are **not** currently scheduled, recorded so they are not
rediscovered from scratch. Each entry states the observed behavior, the code that causes
it, the user-visible consequence, and a sketch of the fix. Verified against the code on the
date noted; re-confirm before acting.

Fixed entries stay, marked `✅ DONE (date)`, so the history reads in one place.

---

## Remote URL normalization (`core.NormalizeRemote`, `internal/core/source.go`)

**Found 2026-07-16**, while fixing the case-folding bug in the same function. All three
items below were confirmed by probing the real function — they are pre-existing and were
**not** introduced by that change.

Background: `NormalizeRemote` reduces a git remote to a comparable form so that one repo
typed several ways reads as one Source. It decides identity in `SrcIdentity.Matches`
(`internal/core/source.go`), which tells a same-source refresh from a `--as` collision on
install, and in `lockEntryMatches` (`cmd/import.go`), which tells "already managed" from a
genuine name collision on import; `MergeEntry` also uses it to keep a recorded remote's
spelling. When it under-normalizes, one repo reads as two
Sources: the user hits a spurious "already installed from a different source" error and,
if they follow the suggestion and pass `--as`, ends up with a duplicate install of the
same repo under a second Skill ID.

### 1. Credentials in a remote URL are recorded in plaintext and leak to the terminal

The most serious of the three — a security issue, not just a correctness one.

`CanonicalRemote` (`internal/core/source.go`) only trims trailing slashes, so a remote typed with
embedded credentials is recorded **verbatim** as the entry's `Source`:

```
CanonicalRemote("https://user:tok3N@github.com/o/r/") = "https://user:tok3N@github.com/o/r"
```

Consequences, all confirmed in code:

- The token lands in `~/.skillm/state.toml`, which is written **`0o644`** —
  world-readable (`internal/state/state.go`).
- `core.SourceLabel` (`internal/core/check.go`) renders `e.Source` as the Source column, so
  `skillm list` **prints the token** to the terminal, into scrollback, and into any CI log.
- `update`, `check` and a re-fetch by Skill ID hand `e.Source` back to `gitx.TreelessClone`
  (`cmd/update.go`, `internal/core/check.go`, `core.RefetchSkill` in
  `internal/core/source.go`), so the stored credential keeps being used.

Separately, `NormalizeRemote` does not strip the `user:token@` userinfo, so the same repo
installed once with and once without credentials reads as two Sources:

```
NormalizeRemote("https://user:tok3N@github.com/o/r") = "user:tok3n@github.com/o/r"
NormalizeRemote("https://github.com/o/r")            = "github.com/o/r"
```

**Fix sketch:** strip userinfo in `CanonicalRemote` before the URL is ever recorded (git
credentials belong in a credential helper or `~/.netrc`, not the registry), and strip it in
`NormalizeRemote` so the two spellings compare equal. Tightening `state.toml` to `0o600` is
worth doing regardless of this entry. Note that stripping at record time is a behavior
change for anyone relying on an embedded token to authenticate `update` — decide whether to
migrate existing entries or just stop recording new ones.

### 2. `ssh://` with an explicit port folds the port into the repo path

`NormalizeRemote` replaces the first `:` with `/` to fold the scp-like form, which also
rewrites a port separator:

```
NormalizeRemote("ssh://git@host.example.com:22/o/r.git") = "host.example.com/22/o/r"
NormalizeRemote("ssh://git@host.example.com/o/r.git")    = "host.example.com/o/r"
```

Same repo, two Sources — the port is not part of a repo's identity, and `:22` is the
default anyway.

**Fix sketch:** fold the scp form only when the text after `:` is not a bare port (scp
syntax has no port; `ssh://` carries one), or parse `ssh://` with `net/url` and drop the
port. Keep the plain scp path working — `git@host:o/r.git` is the common spelling.

### 3. The scp-like form is only recognized for the `git@` user

The fold is gated on a literal `git@` prefix, so any other SSH user misses it:

```
NormalizeRemote("me@host.example.com:o/r.git")       = "me@host.example.com:o/r"
NormalizeRemote("ssh://me@host.example.com/o/r.git") = "me@host.example.com/o/r"
```

Same repo, two Sources. `git@` covers GitHub/GitLab/Bitbucket, so this only bites
self-managed hosts with a per-user or custom SSH account.

**Fix sketch:** match any `user@` prefix rather than the literal `git@`. Beware of
over-matching — this must not swallow the `user:token@` userinfo of an HTTPS URL, which is
entry 1's problem and wants stripping, not folding.

---

## Related

- The host-aware path case-folding rule these three sit alongside is documented at
  `pathCaseInsensitiveHosts` (`internal/core/source.go`); adding a host there is the intended
  extension point when a provider is confirmed case-insensitive.
