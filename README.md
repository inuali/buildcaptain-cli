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

`trigger_run` waits for the build to finish and reports the result, so a single tool call is usually the whole interaction. If it comes back with `"timed_out": true` the build is still going — pass the `run_id` from the result to `get_run_status`, adding `"wait_seconds"` to block until it finishes instead of polling in a loop. Pass `"wait": false` to `trigger_run` to return as soon as the build is accepted.

## Manual use

```sh
buildcaptain configs                        # what this repository is allowed to start
buildcaptain run deploy-staging             # start it and wait for the result
buildcaptain run deploy-staging --no-wait   # start it and print a run ID to poll
buildcaptain wait run_1a2b3c                # block on a running build until it finishes
buildcaptain status run_1a2b3c              # report on it once and return
buildcaptain version
```

`wait` and `status` both default to the most recent run when you omit the ID.

Supply inputs with `--input NAME=VALUE`, once per input. `buildcaptain configs` prints the exact `run` command for each configuration, placeholders included.

Inside a Git repository with an `origin` remote, the app offers the configurations registered for that repository plus any-repository ones; elsewhere only the any-repository configurations are available. In a fork clone (where `origin` points at your fork, not the repository a configuration names), set `BUILDCAPTAIN_REPO=github.com/acme/widgets` to claim the upstream identity.

### Run IDs

`run` and `status` print one identifier, `run_id`, and it is the only value you ever pass back:

```
run_1a2b3c4d5e6f4a8b9c0d1e2f3a4b5c6d
```

Any unambiguous prefix of at least six characters works, as with Git commits — `buildcaptain status run_1a2b3c`. Omitting it entirely uses the most recent run started from this Mac; that is a convenience, not a guarantee, so keep the ID when several builds may be in flight. The answer always names the run it came from in `run_id`.

The values under `jenkins` (`queue_id`, `build_number`, and their URLs) are links for people. They are not accepted by `wait` or `status`.

### Running under a short command timeout

If your shell, CI step, or agent harness kills commands after a fixed window, a blocking `run` can be cut short. It stays recoverable: the run ID and the command that resumes from it go to stderr the moment the build is accepted, before any waiting.

```
deploy-staging: started run_1a2b3c4d… — resume with: buildcaptain wait run_1a2b3c4d…
```

So either resume with `buildcaptain wait <run-id>` after a kill, or stay inside the window deliberately:

```sh
buildcaptain run deploy-staging --timeout 90s   # exits 3 if still running
buildcaptain wait run_1a2b3c --timeout 90s      # resume, as often as needed
```

`run --no-wait` remains the fire-and-forget form. The default stays blocking regardless of whether stdout is a terminal, so the same command means the same thing in a script as at a prompt.

### Output

Every command prints exactly one JSON document on stdout — the result, or `{"ok": false, "error": ...}` — so it can be parsed without filtering. Progress lines while `run` waits go to stderr; `--quiet` suppresses them. `--verbose` adds the raw gateway response under `raw`.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | the build succeeded, or `--no-wait` accepted it |
| 1 | the request failed: not allowed, app unreachable, bad arguments |
| 2 | the build finished unsuccessfully, or approval was rejected |
| 3 | `--timeout` elapsed; the build is still running |

`run` waits up to 30 minutes by default. `--timeout 0` waits indefinitely.

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
