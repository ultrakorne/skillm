# Core — Settings

## Overview

`config get [key]` and `config set <key> <value>` (`cmd/config.go`) read and change the settings
a GUI offers, stored in `config.toml` beside the Agent definitions. The keys, their defaults and
bounds, and their parsing live in `internal/config/settings.go`: the `[refresh]` table,
`refresh.enabled` and `refresh.interval_hours`, the schedule `skillm refresh --if-due` follows
([refresh-status](../refresh-status/DESIGN.md)). Agents are changed through `skillm agent`, never
through `config`. Both commands work without git, so a GUI can show its Settings before it can
report git missing.

## Noteworthy

### A missing setting is its default, everywhere

`config.Load` returns a `Config` whose `Refresh` may be nil or partial; `RefreshEnabled` and
`RefreshIntervalHours` supply the default for anything absent, so a `config.toml` written before
the table existed works unchanged. A fresh Home seeds the table with the defaults, since
`config.Default` includes it. `config get` always reports the effective value, never "unset".

### A bad hand edit reads as the default, never as a broken config

`Load` decodes `[refresh]` loosely: a key of the wrong type (`interval_hours = "12"`, `enabled =
"yes"`) counts as absent. A strict decode would make every command fail, including the
`config set` that repairs it. An `interval_hours` outside 1 to 720 also reads as 24, so a hand
edit to 0 can never make a GUI check in a loop; `Set` refuses the same values with an
`*InvalidValueError`.

### `config set` validates first, then rewrites the whole file under the lock

The key and value are checked against a default `Config` before waiting for the Home lock, so a
typo fails at once and changes nothing. Under the lock it loads, sets and saves: `config.Save`
writes the whole file, keeping the agents and settings but dropping comments and keys skillm does
not model, which is why only an explicit user action (`agent`, `config set`) ever saves Config.
`config get` takes no lock, since every save replaces the file in one step.

### Two typed errors, two protocol codes

An unknown key is a `*config.UnknownKeyError` (its message lists the known keys) and a refused
value a `*config.InvalidValueError`; `internal/protocol/errors.go` maps them to `unknown_key` and
`invalid_value`. A new key needs its constant, `Keys`, `Get`, `Set`, and the `ConfigData` type in
`internal/protocol/data_settings.go` with its fixture, all in one change.

## Integration

`internal/config/settings_test.go` covers the defaults when absent, the seeded table, the
get/set round trip, the refused inputs, out-of-range and wrong-typed hand edits.
`cmd/json_settings_test.go` runs `config get`/`set --json` on the built binary with no git.
