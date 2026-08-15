# KeyBridge

KeyBridge integrates WSL into the same Windows security infrastructure your desktop SSH clients already use — the Windows Certificate Store, CryptoAPI/CNG, and PIV smart cards. It gives WSL a path to that infrastructure through the same agent and the same signing calls a native Windows SSH client already relies on.

KeyBridge is one Windows executable that does two jobs:

1. **SSH agent**, backed by the Windows Certificate Store via CryptoAPI/CNG. Any certificate with a usable private key — a plain user cert, a PIV smart card, a Windows Hello-backed key — becomes an SSH identity automatically. Signing happens on the Windows side; the PIN/touch prompt you already know is entirely Windows'.
2. **Named-pipe relay**, so that same agent (or any other Windows named pipe) can be reached from a WSL shell. This is the same job [npiperelay](https://github.com/jstarks/npiperelay) does, built into the same binary.

It's a fork of [buptczq/WinCryptSSHAgent](https://github.com/buptczq/WinCryptSSHAgent) — job 1 above is entirely their work, unmodified. Job 2 is new here.

![Architecture: WSL tools -> socat -> keybridge.exe relay -> named pipe -> keybridge.exe agent -> Windows CryptoAPI/CNG -> cert store / PIV smart card, and separately keybridge.exe agent -> SSH server](overview.svg)

## Why this exists

SSH clients running directly on Windows already authenticate through the Windows Certificate Store and CryptoAPI/CNG — including PIV smart cards, where the private key is generated on the card and never leaves it. WSL runs alongside Windows on the same machine but isn't part of that infrastructure by default: it has no path of its own to a Windows smart card reader or the certificate store.

KeyBridge is that path. It gives WSL access to the same agent and the same signing calls a native Windows SSH client already uses — same PIN prompt, same CryptoAPI/CNG, same key that never leaves the card. Previously, doing this meant running two separate tools — an agent that can talk to the smart card (WinCryptSSHAgent) and a relay that pipes a Windows named pipe into a WSL Unix socket (npiperelay), wired together with `socat`. KeyBridge is those two collapsed into one binary: one thing to install, one thing to keep running, one set of docs to read.

## Features

* SSH identities sourced directly from the Windows Certificate Store — no separate key files, no separate driver install for PIV smart cards
* OpenSSH certificate support via a filename-based override mechanism (see below)
* Speaks five different Windows SSH-agent transports (Cygwin socket, Windows AF_UNIX socket, named pipe, Pageant protocol, XShell Xagent), so it works with most Windows SSH clients, not just WSL
* Built-in named-pipe relay (`keybridge.exe relay ...`) — reach its own agent pipe, or any other Windows named pipe, from WSL

## Quick start: SSH agent

1. Run `keybridge.exe`. It sits in the system tray — no window.
2. Right-click the tray icon to see connection info for whichever transport your SSH client needs (Pageant, named pipe, etc.). Most Windows SSH clients (PuTTY, XShell, JetBrains, Git for Windows) will just find it automatically via Pageant protocol; nothing to configure.
3. For WSL specifically, see the relay section below.

Setting up a PIV smart card (e.g. a YubiKey) for the first time is a separate, more involved process — see [`doc/wsl_tutorial.md`](doc/wsl_tutorial.md).

## Using the named-pipe relay

This is the part that gets KeyBridge into WSL. The idea: `keybridge.exe` (running on Windows) listens on a named pipe; `keybridge.exe relay` (also on Windows, but invoked *from* WSL) connects to that pipe and shuttles bytes over stdin/stdout; `socat`, running in WSL, turns that into a normal Unix socket that `ssh` and everything else already know how to use.

```
$WSL app (ssh, oc, aws, ...)
        |  reads/writes SSH_AUTH_SOCK
        v
socat UNIX-LISTEN:$SSH_AUTH_SOCK,fork
        |  spawns, per connection
        v
keybridge.exe relay //./pipe/openssh-ssh-agent   <- runs as a Windows process, via WSL interop
        |  named pipe
        v
keybridge.exe (the agent)  --CryptoAPI/CNG-->  Cert Store / PIV smart card
```

### 1. Get `keybridge.exe` reachable from WSL

`keybridge.exe` has to run as a *Windows* process — named pipes are a Win32 API WSL can't touch directly. Put it somewhere on the Windows filesystem and make sure WSL can find it on `PATH`:

```bash
# from WSL, assuming keybridge.exe lives in C:\Users\<you>\bin\
echo 'export PATH="$PATH:/mnt/c/Users/<you>/bin"' >> ~/.bashrc
```

### 2. Install `socat` in WSL

```bash
sudo apt install socat   # or your distro's equivalent
```

### 3. Start the relay

The agent's named pipe path is fixed: `\\.\pipe\openssh-ssh-agent` (Windows-side syntax) — from WSL, forward slashes work fine: `//./pipe/openssh-ssh-agent`.

```bash
export SSH_AUTH_SOCK="$HOME/.ssh/agent.sock"
rm -f "$SSH_AUTH_SOCK"
(setsid socat UNIX-LISTEN:"$SSH_AUTH_SOCK",fork EXEC:"keybridge.exe relay //./pipe/openssh-ssh-agent" >/dev/null 2>&1 &)
```

Put those three lines in `~/.bashrc` (or equivalent) so `SSH_AUTH_SOCK` and the relay are set up in every new shell. The `setsid` detaches it from the shell so it survives after the script that started it exits; check first that nothing's already listening on the socket if you want to avoid stacking up duplicate `socat` processes on every new shell (`ss -lx | grep -q "$SSH_AUTH_SOCK" || (setsid socat ... &)`).

### 4. Test it

```bash
ssh-add -l          # should list the certs keybridge.exe has loaded
ssh -T git@github.com   # or any host set up with your public key
```

The Windows PIN/touch prompt you're used to will pop up exactly as it does for any other Windows SSH client — KeyBridge doesn't change that part at all.

### Relay flags

```
keybridge.exe relay [-p] [-s] [-ep] [-ei] [-v] <named pipe path>
```

| Flag | Meaning |
|------|---------|
| `-p`  | if the pipe doesn't exist yet, keep retrying instead of exiting right away |
| `-s`  | once stdin closes, write a zero-length message to mark end-of-data on a message-mode pipe |
| `-ep` | exit as soon as the pipe side closes, without waiting for the stdin side |
| `-ei` | exit as soon as stdin closes, without waiting for the pipe side |
| `-v`  | log connection and shutdown events to stderr |

These match `npiperelay`'s flags, so if you already have `socat ... EXEC:"npiperelay.exe ..."` commands lying around (for Docker Desktop's pipe, a Windows MySQL service, a Hyper-V serial console, etc.), they work unchanged with `keybridge.exe relay` swapped in — one relay binary instead of two.

## OpenSSH certificates

OpenSSH certificates aren't the same format as the X.509 certificates in the Windows Certificate Store, so KeyBridge can't convert one into the other automatically. Instead, drop your OpenSSH certificate into your Windows user profile folder, named `<Certificate Common Name>-cert.pub` or `<Certificate Serial Number>-cert.pub`, and KeyBridge will pair it with the matching store certificate automatically.

## Debug logging

```
setx WCSA_DEBUG 1
```

then reboot (environment variable changes need a fresh process tree to take effect reliably here). Reproduce the problem; the log lands at `%USERPROFILE%\WCSA_DEBUG.log`.

## Building from source

```bash
go build .                                   # on Windows
GOOS=windows GOARCH=amd64 go build .         # cross-compiled from Linux/WSL/macOS
```

No `CGO_ENABLED` requirements, no external toolchain beyond Go itself.

## Acknowledgments

KeyBridge stands entirely on the work of two people who solved the hard parts first, and were generous enough to publish the result.

**John Starks** wrote [npiperelay](https://github.com/jstarks/npiperelay) — a small tool, but a well-made one. Bridging a Win32 named pipe to stdio sounds trivial until you actually sit down and get the overlapped I/O and half-close semantics right; the original source is a clean, careful piece of engineering, and it's the reason a whole ecosystem of WSL-to-Windows tooling exists at all. KeyBridge's relay mode owes its correctness to having that reference to check assumptions against.

**BUPTCZQ** wrote [WinCryptSSHAgent](https://github.com/buptczq/WinCryptSSHAgent), which is the harder problem by a wide margin — a correct, multi-protocol SSH agent built directly on Windows CryptoAPI/CNG, handling smart cards, certificate enumeration, and signing without ever touching a private key. That's the kind of systems code that's easy to get subtly wrong and hard to verify without real hardware in hand; getting it right and open-sourcing it is a genuine gift to anyone who's had to deal with a PIV card and a corporate Windows machine. This entire project is a fork of theirs, unmodified except for the relay addition documented above.

Thank you both for doing quality work in public.

## Credits & License

KeyBridge is a fork of [buptczq/WinCryptSSHAgent](https://github.com/buptczq/WinCryptSSHAgent), licensed under the [Apache License 2.0](LICENSE) (Copyright 2019 BUPTCZQ). All SSH-agent, CryptoAPI/CNG signing, and tray-app code is theirs; `main.go` carries a notice marking the one addition made to it, per the license's requirements.

The relay subcommand replicates the *behavior* (and CLI flags) of [jstarks/npiperelay](https://github.com/jstarks/npiperelay) (MIT License, © 2017 John Starks) as a design reference, but is an independent implementation written against `go-winio` — no npiperelay source was copied.
