# openhued

Minimal Unix-socket Hue daemon plus CLI helper built on `github.com/openhue/openhue-go`. Run `openhued serve` once, then send lightweight commands (`toggle`, `up`, `down`, `status`) to the daemon without repeatedly hitting the bridge.

## Requirements
- Go 1.24+ (per `go.mod`).
- Hue bridge credentials set up for `openhue-go` (e.g., the usual `~/.config/openhue/config.json` with bridge IP and API key created via `openhue`).
- A Hue grouped light id you want to control.

## Build & Install
- Go toolchain: `go install github.com/PTRK-DE/openhued@latest` (drops `openhued` in `~/go/bin`).
- Local build: `go build .` (outputs `./openhued`).
- Installer: `./Install.sh [/install/path/openhued]` builds into a temp dir and installs with correct permissions. Default target is `/usr/local/bin/openhued`; sudo is used automatically only if needed for the target directory.

## Configure the daemon
`openhued serve` loads JSON config from `~/.config/openhued/daemon.json` by default (or a path passed via `-config`).

```json
{
  "light_id": "your-grouped-light-id",
  "brightness_increment": 5,
  "stream_brightness": false
}
```

Fields:
- `light_id` (required): grouped light identifier to control.
- `brightness_increment` (optional): brightness step in percent for `up/down` commands (defaults to 5; clamped 0–100).
- `stream_brightness` (optional): when true the daemon prints the current brightness to stdout after each command, useful for status bars like Waybar (defaults to false).

## Run
1) Start the daemon (single terminal):
```
./openhued serve -config ~/.config/openhued/daemon.json [-socket /custom/path.sock]
```
- Socket defaults to `$XDG_RUNTIME_DIR/openhued-<uid>.sock` (falls back to `/tmp/...`).
- The daemon connects to the Hue event stream to keep local state synced and prints the socket path.

2) Send commands from any shell (reuse the same socket path if you changed it):
```
./openhued toggle   # toggle on/off
./openhued up       # brighten by increment
./openhued down     # dim by increment
./openhued status   # print cached brightness percent
```
Notes:
- `status` is read-only and uses the cached state (no bridge request).

## Development
- Run `go test ./...` to execute tests (none present yet but keeps `go.mod` deps tidy).
- The event-stream loop currently reconnects with a fixed 5s backoff; see `OPTIMIZATION_NOTES.md` for future tuning ideas.

## License
Licensed under the Apache License 2.0. See [`LICENSE`](LICENSE) for details.
