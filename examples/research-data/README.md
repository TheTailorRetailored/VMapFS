# Research-data views

This example shows how two VMapFS mounts can present the same immutable source tree through different directory organisations.

Assume the source archive is controlled by an acquisition system:

```text
source/
  instrument-a/2026-01-15/run-001.csv
  instrument-a/2026-02-03/run-002.csv
  instrument-b/2026-01-18/run-003.parquet
```

`by-project.json` presents the files as:

```text
projects/
  climate/run-001.csv
  climate/run-003.parquet
  calibration/run-002.csv
```

`by-date.json` presents the same files as:

```text
2026/
  01/run-001.csv
  01/run-003.parquet
  02/run-002.csv
```

Run each view with a separate mount point and state file:

```bash
vmapfs -source "$PWD/source" -mount /tmp/research-by-project -state "$PWD/by-project.json"
vmapfs -source "$PWD/source" -mount /tmp/research-by-date -state "$PWD/by-date.json"
```

The example profiles are intentionally synthetic. In normal use, mappings are also created by moving files from `_UNSORTED` into virtual directories through the mounted filesystem.
