# VMapFS

VMapFS is a small Go/FUSE experiment for rearranging a directory tree without
touching the original files. You point it at a read-only source directory and
give it a JSON state file; it mounts the alternate layout you described.

Virtual directories, renamed paths and extended attributes live in the state
file. The source tree stays where it is.

## The idea

I wanted to reorganise awkward source trees without breaking checksums, sync
jobs or anything else that expected the old paths. A FUSE view turned out to be
a useful way to do that.

Useful examples include:

- presenting research data by project while retaining an instrument-native source layout;
- giving legacy archives a clearer directory structure without a migration;
- creating separate read-only views of the same source using different state profiles;
- exposing unmapped files through `_UNSORTED` so new arrivals remain visible.

## Status

This is still experimental, not a production filesystem. It has unit tests and
Linux CI, but I have only used it in a small number of FUSE environments. Try
it on non-critical data first and keep a copy of the state file.

## Features

- immutable source-data access;
- persistent virtual path mappings and directories;
- `_UNSORTED` discovery for files without mappings;
- state backups retaining the five most recent versions;
- configurable UID/GID and extended-attribute support;
- direct streaming from source files;
- debug logging for filesystem operations.

## Architecture

```text
applications
    |
FUSE mount (virtual paths)
    |
VMapFS path mapper ---- JSON state + backups
    |
read-only source tree
```

Each mount uses one state file. To expose the same source in two different organisations, run two mounts with separate state profiles.

## Requirements

- Linux with FUSE support;
- Go 1.21 or later for source builds;
- read access to the source directory;
- write access to the mount point and state-file directory.

On Debian or Ubuntu:

```bash
sudo apt-get install fuse libfuse-dev
```

## Build and test

```bash
git clone https://github.com/TheTailorRetailored/VMapFS.git
cd VMapFS
make test
make build
```

The binary is written to `bin/vmapfs`.

## Usage

```bash
mkdir -p /tmp/vmapfs-view
./bin/vmapfs \
  -source /path/to/source \
  -mount /tmp/vmapfs-view \
  -state /path/to/state.json
```

Environment variables:

- `PUID` and `PGID` control presented ownership;
- `FUSE_DEBUG=1` enables FUSE diagnostics;
- `LOG_LEVEL=DEBUG` increases application logging.

The state format maps source-relative paths to virtual paths:

```json
{
  "mappings": {
    "instrument-a/run-001.csv": {
      "virtual_path": "/projects/climate/run-001.csv"
    }
  },
  "directories": {
    "/": true,
    "/projects": true,
    "/projects/climate": true
  },
  "version": 1
}
```

See [`examples/research-data`](examples/research-data/README.md) for two state profiles that present the same synthetic source tree in different ways.

## Virtual operations

- Browse `_UNSORTED` to find source files that have not been mapped.
- Create virtual directories through the mounted filesystem.
- Move or rename files within the virtual view without altering their source paths.
- Remove a virtual mapping without deleting the source file.

State changes are persisted with restricted file permissions. Backups are stored beside the state file in `.vmapfs-backups/`.

## Limitations

- Linux/FUSE is the primary supported environment.
- A source file has one virtual path per mounted state profile.
- Concurrent external edits to a state file are unsupported.
- Compatibility and crash behaviour have not been validated for production workloads.

## Development

```bash
make test
go test -race ./...
go vet ./...
```

Pull requests should include tests for changes to path mapping, state persistence, filesystem operations, or extended attributes.

## Security

VMapFS is designed to preserve source data, but a filesystem bug can still cause unexpected behaviour. Mount only directories you intend to expose, avoid privileged execution, review the generated state, and maintain independent backups of important data.

Please report security concerns privately to the repository owner rather than attaching sensitive paths or state files to a public issue.

## Licence

MIT. See [`LICENSE`](LICENSE).
