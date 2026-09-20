# skillm — Known Issues & Deferred Work

Confirmed defects that are **not** currently scheduled, recorded so they are not
rediscovered from scratch. Each entry states the observed behavior, the code that causes
it, the user-visible consequence, and a sketch of the fix. Verified against the code on the
date noted; re-confirm before acting.

Fixed entries stay, marked `✅ DONE (date)`, so the history reads in one place.

---

## Remote URL normalization (`normalizeRemote`, `cmd/import.go`)

**Found 2026-07-16**, while fixing the case-folding bug in the same function. All three
items below were confirmed by probing the real function — they are pre-existing and were
**not** introduced by that change.

Background: `normalizeRemote` reduces a git remote to a comparable form so that one repo
typed several ways reads as one Source. It has two callers — `srcIdentity.matches`
(`cmd/fetch.go`), which decides whether an install is a same-source refresh or a
`--as` collision, and `lockEntryMatches` (`cmd/import.go`), which tells "already managed"
from a genuine name collision on import. When it under-normalizes, one repo reads as two
Sources: the user hits a spurious "already installed from a different source" error and,
if they follow the suggestion and pass `--as`, ends up with a duplicate install of the
same repo under a second Skill ID.

### 1. Credentials in a remote URL are recorded in plaintext and leak to the terminal

The most serious of the three — a security issue, not just a correctness one.

`canonicalRemote` (`cmd/fetch.go`) only trims trailing slashes, so a remote typed with
embedded credentials is recorded **verbatim** as the entry's `Source`:

```
canonicalRemote("https://user:tok3N@github.com/o/r/") = "https://user:tok3N@github.com/o/r"
```

Consequences, all confirmed in code:

- The token lands in `~/.skillm/state.toml`, which is written **`0o644`** —
  world-readable (`internal/state/state.go:121`).
- `sourceLabel` (`cmd/list.go:145-147`) renders `e.Source` as the Source column, so
  `skillm list` **prints the token** to the terminal, into scrollback, and into any CI log.
- `update` and `list --check` hand `e.Source` back to `gitx.TreelessClone`
  (`cmd/update.go:369`, `cmd/list.go:121`), so the stored credential keeps being used.

Separately, `normalizeRemote` does not strip the `user:token@` userinfo, so the same repo
installed once with and once without credentials reads as two Sources:

```
normalizeRemote("https://user:tok3N@github.com/o/r") = "user:tok3n@github.com/o/r"
normalizeRemote("https://github.com/o/r")            = "github.com/o/r"
```

**Fix sketch:** strip userinfo in `canonicalRemote` before the URL is ever recorded (git
credentials belong in a credential helper or `~/.netrc`, not the registry), and strip it in
`normalizeRemote` so the two spellings compare equal. Tightening `state.toml` to `0o600` is
worth doing regardless of this entry. Note that stripping at record time is a behavior
change for anyone relying on an embedded token to authenticate `update` — decide whether to
migrate existing entries or just stop recording new ones.

### 2. `ssh://` with an explicit port folds the port into the repo path

`normalizeRemote` replaces the first `:` with `/` to fold the scp-like form, which also
rewrites a port separator:

```
normalizeRemote("ssh://git@host.example.com:22/o/r.git") = "host.example.com/22/o/r"
normalizeRemote("ssh://git@host.example.com/o/r.git")    = "host.example.com/o/r"
```

Same repo, two Sources — the port is not part of a repo's identity, and `:22` is the
default anyway.

**Fix sketch:** fold the scp form only when the text after `:` is not a bare port (scp
syntax has no port; `ssh://` carries one), or parse `ssh://` with `net/url` and drop the
port. Keep the plain scp path working — `git@host:o/r.git` is the common spelling.

### 3. The scp-like form is only recognized for the `git@` user

The fold is gated on a literal `git@` prefix, so any other SSH user misses it:

```
normalizeRemote("me@host.example.com:o/r.git")       = "me@host.example.com:o/r"
normalizeRemote("ssh://me@host.example.com/o/r.git") = "me@host.example.com/o/r"
```

Same repo, two Sources. `git@` covers GitHub/GitLab/Bitbucket, so this only bites
self-managed hosts with a per-user or custom SSH account.

**Fix sketch:** match any `user@` prefix rather than the literal `git@`. Beware of
over-matching — this must not swallow the `user:token@` userinfo of an HTTPS URL, which is
entry 1's problem and wants stripping, not folding.

---

## Related

- The host-aware path case-folding rule these three sit alongside is documented at
  `pathCaseInsensitiveHosts` (`cmd/import.go`); adding a host there is the intended
  extension point when a provider is confirmed case-insensitive.
