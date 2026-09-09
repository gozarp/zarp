# Changelog

One file per release, named for its version.

| Version | Date | Notes |
|---|---|---|
| [v0.1.0](CHANGELOG-0.1.0.md) | 2026-09-09 | First tagged release |

## Adding a release

Create `CHANGELOG-<version>.md` — matching the tag without its `v` prefix, so `v0.2.0` becomes
`CHANGELOG-0.2.0.md` — and add a row to the table above, newest first.

Write it for someone deciding whether to upgrade. Lead with what breaks, because that is what
costs them time, and say why each change was made rather than only what changed. Group by what the
reader cares about — security defaults, correctness, API shape — not by commit order. Record the
verification the release actually passed, and be exact about anything you did not re-run.

While zarp is pre-`v1.0.0`, breaking changes are allowed in any release and belong at the top of
the file.
