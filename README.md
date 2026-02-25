# SAX

A terminal multiplexer with client-server architecture, written in Go. Like GNU Screen and tmux, but simpler.

Sessions persist in the background. Detach, close the terminal, come back later and reattach. All your shells and processes keep running.

## Install

**Linux / macOS** (one-liner):

```bash
curl -fsSL https://raw.githubusercontent.com/ASCtheone/sax/main/install.sh | bash
```

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/ASCtheone/sax/main/install.ps1 | iex
```

**Manual**: Download the latest binary from [Releases](https://github.com/ASCtheone/sax/releases) and add it to your PATH.

**From source**:

```bash
go install github.com/asc/sax@latest
```

## Quick Start

```bash
sax                        # interactive session picker (creates "default" if none exist)
sax -ca work               # create session "work" and attach
# ... do stuff ...
# Ctrl+S d                 # detach (session keeps running)
sax -a work                # reattach later
```

## Usage

```
sax                          interactive session picker
sax -a <name>                attach to session
sax -c <name>                create session (detached)
sax -ca <name>               create + attach
sax -x <name> <command...>   run command in new session (detached)
sax -xa <name> <command...>  run command + attach
sax -l, --list               list active sessions
sax --kill <name>            kill a session
sax --kill-all               kill all sessions
sax update                   update to latest release
sax --version, -v            show version
```

## Keybindings

All commands use the prefix **Ctrl+S**, then a command key.

### Tabs

| Key | Action |
|-----|--------|
| `c` | New tab |
| `n` / `p` | Next / Previous tab |
| `1`-`9` | Go to tab N |
| `X` | Close tab |
| `"` | Window list |

### Panes

| Key | Action |
|-----|--------|
| `v` or `\|` | Split vertical |
| `s` or `-` | Split horizontal |
| `h/j/k/l` | Navigate panes (vim-style) |
| `x` | Close pane |
| `z` | Zoom/unzoom pane |

### Session

| Key | Action |
|-----|--------|
| `d` | Detach |
| `[` | Enter copy/scrollback mode |
| `]` | Paste from copy buffer |
| `H` | Toggle pane logging |
| `Ctrl+x` | Lock session |
| `M` | Monitor activity |
| `_` | Monitor silence |
| `?` | Toggle command panel |

### Copy Mode

Enter with `Ctrl+S [`. Navigate with vim keys:

| Key | Action |
|-----|--------|
| `h/j/k/l` | Move cursor |
| `Ctrl+F/B` | Page down/up |
| `g` / `G` | Top / Bottom |
| `Space` | Set mark |
| `y` | Yank selection |
| `/` | Search |
| `q` / `Esc` | Exit copy mode |

## NX Workspace

SAX can discover and launch dev servers from NX monorepo workspaces.

```bash
sax nx                       # list projects with serve targets
sax nx serve                 # interactive picker
sax nx serve myapp           # serve a specific app
sax nx myapp                 # shorthand for above
sax nx stop myapp            # stop an app
sax nx stop                  # stop all NX sessions
```

SAX scans for `nx.json` and `project.json` files, finds targets named `serve`, `dev`, or `start`, and runs them as detached sessions named after the project.

## Agent / Scripting API

Programmatic access for CI, scripts, and AI agents:

```bash
sax --tail <name> [n]        # last N lines from active pane (default 10)
sax --send <name> <text>     # send text to active pane
sax --status <name>          # session status as JSON
```

Examples:

```bash
# Start a dev server and check its output
sax -x api node server.js
sax --tail api 20

# Send a command to a running session
sax --send api "npm test\n"

# Get structured session info
sax --status api
```

## Updating

SAX checks for updates once daily in the background and prints a notice to stderr if a new version is available. To update:

```bash
sax update
```

To check the current version:

```bash
sax --version
```

## Architecture

SAX uses a client-server model:

- **Server daemon** owns PTYs, sessions, terminal emulation, and rendering. It persists when clients disconnect.
- **Thin client** (bubbletea TUI) connects over IPC, sends keystrokes, and displays pre-rendered frames from the server.
- **IPC** uses Unix domain sockets on Linux/macOS and named pipes on Windows.

```
CLIENT                              SERVER (daemon)
+------------------+   IPC socket   +----------------------------+
| bubbletea TUI    |<---Frame------| renderer                   |
| View() = frame   |               |   tabbar + panes + borders |
| prefix mode FSM  |---KeyInput--->|   + statusbar              |
|                  |---Resize----->|                            |
|                  |---Command---->| session -> tabs -> panes   |
|                  |               |   pty + terminal emulator  |
+------------------+               +----------------------------+
```

## Platforms

- Linux (amd64, arm64)
- macOS (amd64, arm64)
- Windows (amd64)

## License

MIT
