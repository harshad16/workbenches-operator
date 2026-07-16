# `opt/manifests`

This directory contains upstream component manifests fetched by `get_all_manifests.sh`.

**Do not edit files under `opt/manifests/` manually.**

Manifests are refreshed automatically by the scheduled GitHub Action (`.github/workflows/manifest-sync.yaml`) or locally via:

```shell
make manifests-fetch
```

Commit changes only after running the fetch script. See `DEPENDENCIES.md` for upstream source configuration and upgrade steps.
