# frigatecfg

Two owners for one Frigate `config.yml`, split by key path:

- **git owns structure** -- streams, roles, storage, detectors, models.
  The *canonical* file.  Edited only in git.
- **the Frigate web UI owns tuning** -- motion masks, zones, motion
  thresholds, object masks, review zones, camera groups, enrichment
  toggles: exactly the dotted paths the UI writes through
  `PUT /api/config/set` (enumerated from Frigate v0.17.0's `web/src`),
  plus `version`, which Frigate's startup migration rewrites.  The
  *tuning* file holds git's copy of them; the live file on Frigate's
  volume holds the UI's.

Everything not under an owned path is structural.  `frigatecfg paths`
prints the owned list.

## Commands

    frigatecfg render -canonical F -tuning F [-out F]
        canonical + tuning.  Deterministic.  The FIRST-start config.

    frigatecfg merge -canonical F -tuning F -live F [-out F]
        Structure from canonical; each owned path from live if present,
        else from tuning, else whatever canonical carried.  The
        EVERY-start config.  Owned paths absent from live are not
        removed: the UI cannot express delete, so absence is not a
        decision.  -out may equal -live (written atomically).

    frigatecfg pull -live F [-out F]
        The owned paths present in live, as a tuning file.  Commit the
        diff.  Secrets never travel: Frigate keeps {FRIGATE_*}
        placeholders in the file and substitutes at runtime.

    frigatecfg diff -canonical F -tuning F -live F
        Classifies every difference.  "tuning" = pull it when
        convenient; "STRUCTURAL" = made in the UI, will be reverted on
        the next start, move it to git.  Exit 0 / 2 (tuning only) / 3
        (structural).

A missing tuning file is an empty one (a camera with no UI tuning yet
is the normal case).  Comments in the canonical file survive `render`
and `merge`; Frigate's own writer (ruamel.yaml, round-trip mode)
preserves them too, so a commented config stays commented through UI
edits.

## As an init container

    frigatecfg merge -canonical /in/garage.yaml -tuning /in/garage.tuning.yaml \
        -live /config/config.yml -out /config/config.yml

A nonexistent `-live` is an empty one, so this single command is the
first start (renders from git, no seeding step) and every later start
(re-asserts git's structure, keeps the UI's tuning).

Mount the two git files (a ConfigMap) at `/in` and Frigate's state
volume at `/config`; the Frigate container mounts only `/config`.

The image is `FROM scratch` with the static binary at `/frigatecfg`
and no shell; the pod spec calls it with `args`.

## Why not text markers

Frigate's `/api/config/set` sets a dotted key wherever it lives; it
does not respect regions of a file.  Ownership by path matches what
the writer actually does.  The raw editor (`/api/config/save`) can
write anything, which is why structural drift is detected and
reverted rather than merged.

## Development

Go, one dependency (`gopkg.in/yaml.v3`).  `gofmt`, `go vet`, `go test`
and a secret scan (`lifecycle/presubmit-check.sh`: IPs, keys, long
hex, UUIDs; allowlist beside it) run in CI; `sh lifecycle/install-hooks.sh`
installs the scan as the pre-commit hook.  Pushes to `main` and `v*`
tags publish `ghcr.io/jeffbstewart/frigatecfg`.  7-bit ASCII, LF line
endings.

## License

Apache-2.0; see LICENSE.
