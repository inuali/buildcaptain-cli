# buildcaptain CLI

Command-line companion for the [BuildCaptain](https://github.com/inuali) macOS app. It connects to the app's local agent gateway and lets AI coding agents (Claude Code, Codex, any MCP client) list and trigger the safe Jenkins run configurations you defined in the app — nothing more.

The CLI never talks to Jenkins itself. All requests go through the BuildCaptain app on the same Mac, which enforces per-repository allow-lists, parameter rules, and optional human approval.

## Install

```sh
brew install inuali/tap/buildcaptain
```

Or download a binary from [Releases](https://github.com/inuali/buildcaptain-cli/releases) and put it on your `PATH`.

To build and install the CLI from this repository:

```sh
./install.sh
```

This installs `buildcaptain` in `/usr/local/bin`. Set `INSTALL_DIR` to use a different folder:

```sh
INSTALL_DIR="$HOME/bin" ./install.sh
```

Requirements: macOS 14+, the BuildCaptain app installed, and Agent Access enabled in the app.

## Use with AI agents (MCP)

```sh
claude mcp add buildcaptain -- buildcaptain mcp
codex mcp add buildcaptain -- buildcaptain mcp
```

This exposes three tools: `list_run_configurations`, `trigger_run`, `get_run_status`. Run configurations restricted to a Git repository are matched against the current working directory's `origin` remote; configurations marked "Any repository" in the app are always available.

## Manual use

```sh
buildcaptain configs                                  # list allowed run configurations
buildcaptain run <configuration-id> --input NAME=VALUE
buildcaptain status <operation-id>
buildcaptain version
```

Inside a Git repository with an `origin` remote, the app offers the configurations registered for that repository plus any-repository ones; elsewhere only the any-repository configurations are available. In a fork clone (where `origin` points at your fork, not the repository a configuration names), set `BUILDCAPTAIN_REPO=github.com/acme/widgets` to claim the upstream identity.

## How it works

The CLI connects to a Unix socket the app exposes (inside the app's sandbox container, `~/Library/Containers/com.buildcaptain.app/Data/Library/Application Support/BuildCaptain/AgentGateway/`, or the plain Application Support path for development builds). If the app isn't running, the CLI launches it in the background and retries for a few seconds. Connections are restricted to the same user, and every trigger can require approval in the app.

Set `BUILDCAPTAIN_GATEWAY_SOCKET` to override the socket path (useful for development).

## Development

```sh
./build.sh     # builds ./buildcaptain with the version stamped from git
go test ./...
```

Releases are automated with GoReleaser: pushing a `v*` tag builds a universal macOS binary, publishes a GitHub release, and updates the Homebrew formula in [`inuali/homebrew-tap`](https://github.com/inuali/homebrew-tap).

## License

MIT
