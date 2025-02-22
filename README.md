# VMapFS

A virtual FUSE filesystem that creates a customizable view of files without modifying the source. VMapFS lets you reorganize, rename, and restructure files while keeping the original data untouched.

## Features

- **Virtual Directory Structure**: Create custom hierarchies without altering source files
- **Automatic File Discovery**: Browse unmapped files through the `_UNSORTED` directory
- **State Management**: Persistent mappings with automatic state file backups (keeps last 5 versions)
- **Source Preservation**: Read-only access to source files ensures data integrity
- **Flexible Integration**: Should work with any mounted filesystem (local, NFS, FUSE)
- **Direct I/O**: Efficient streaming of source files
- **Permission Management**: Configurable UID/GID via environment variables
- **Debug Logging**: Optional detailed logging for troubleshooting

**⚠️ Important Note**: This software is currently in early development and has only been tested in limited environments. It should be considered experimental and not ready for production use. Testing and contributions are welcome!

## Use Cases

- **Media Servers**: Organize streaming content (e.g., RealDebrid via Zurg) into clean media libraries
- **Research Data**: Present the same dataset organized by different attributes (e.g., by date, by type, by project) without duplicating files
- **Cloud Storage**: Create custom views of remote storage without syncing
- **Data Organization**: Maintain multiple virtual organizations of the same data
- **Legacy Systems**: Provide modern directory structures over old file layouts

For example, a research dataset could be organized multiple ways:
```
# By Date
/2023/Q1/experiment_a.dat
/2023/Q2/experiment_b.dat

# By Project
/project_alpha/experiment_a.dat
/project_beta/experiment_b.dat

# By Type
/raw_data/experiment_a.dat
/raw_data/experiment_b.dat
```
All pointing to the same source files, allowing different teams to use their preferred organization.

## Requirements

- Linux with FUSE support (`libfuse` or `fuse3`)
- Go 1.21 or later (for building)
- Read access to source filesystem
- Write access to mount point

### Installing FUSE

```bash
# Ubuntu/Debian
sudo apt-get install fuse

# RHEL/CentOS
sudo yum install fuse

# Arch Linux
sudo pacman -S fuse2

# macOS
brew install macfuse
```

## Installation

### From Source

```bash
git clone https://github.com/yourusername/vmapfs.git
cd vmapfs
make build
```

The binary will be created in `bin/vmapfs`.

### Docker

An example Docker setup combining VMapFS with Jellyfin, Zurg (Real-Debrid), and other services will be provided in a separate repository to demonstrate a complete media server stack.

## Usage

### Basic Command

```bash
vmapfs -mount /path/to/mountpoint -source /path/to/source -state /path/to/state.json
```

### Environment Variables

- `PUID`: User ID for file ownership (default: current user)
- `PGID`: Group ID for file ownership (default: current group)
- `FUSE_DEBUG`: Enable detailed FUSE debugging (default: 0)
- `LOG_LEVEL`: Set logging verbosity (default: INFO)

### State File

The state file (`state.json`) maintains your virtual filesystem structure:

```json
{
  "virtual_paths": {
    "/movies/Inception (2010).mkv": "raw/inception_1080p.mkv",
    "/tv/Breaking Bad/S01E01.mkv": "shows/bb_101.mkv"
  },
  "directories": {
    "/": true,
    "/movies": true,
    "/tv": true,
    "/tv/Breaking Bad": true
  },
  "version": 1
}
```

### Directory Structure

- **/** - Root of virtual filesystem
- **/_UNSORTED** - Shows unmapped files from source
- **/your/paths** - Your custom directory structure

### File Operations

- **Browse**: Navigate `_UNSORTED` to see available files
- **Organize**: Create directories and move files as needed
- **Rename**: Rename files or directories without affecting source
- **Remove**: Delete virtual paths without touching source files

### Automatic Features

- State file backups (keeps last 5 versions)
- Automatic source file discovery in _UNSORTED
- Virtual directory cleanup when removing mappings

Note: The _UNSORTED directory is planned to be hidden when empty in future versions.

## Integration Examples

### Media Server Setup

```yaml
# docker-compose.yml example
services:
  vmapfs:
    build: .
    volumes:
      - /mnt/source:/source:ro
      - /mnt/virtual:/virtual
      - ./state:/state
    environment:
      - PUID=1000
      - PGID=1000
    devices:
      - /dev/fuse:/dev/fuse
    cap_add:
      - SYS_ADMIN
    security_opt:
      - apparmor:unconfined
```

### Jellyfin/Plex Integration

Point your media server to the VMapFS mount for a clean library structure while keeping source files organized separately.

## Development

### Building

```bash
make build  # Build binary
make test   # Run tests
make clean  # Clean build artifacts
```

### Testing

```bash
make test
```

### Debugging

1. Enable debug logging:
```bash
export FUSE_DEBUG=1
export LOG_LEVEL=DEBUG
```

2. Check logs:
- FUSE operations: kernel logs (`dmesg`)
- VMapFS logs: stdout/stderr

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

MIT License - See [LICENSE](LICENSE) for details.

## Acknowledgments

- FUSE (Filesystem in Userspace)
- [bazil.org/fuse](https://github.com/bazil/fuse) - Go FUSE library