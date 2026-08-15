# KeyBridge

A fork of [WinCryptSSHAgent](https://github.com/buptczq/WinCryptSSHAgent) with one addition: a built-in `relay` subcommand, so the same binary can also bridge a Windows named pipe to stdin/stdout (the same job [jstarks/npiperelay](https://github.com/jstarks/npiperelay) does) — useful for reaching this binary's own SSH-agent pipe, or any other named pipe (Docker Desktop, a Windows MySQL service, etc.), from WSL via `socat`.

Everything else — SSH-agent behavior backed by Windows CryptoAPI/CNG (including PIV/smart-card certs), the tray app, Pageant/Cygwin/named-pipe/XShell protocol support — is unmodified upstream WinCryptSSHAgent behavior. See [Credits & License](#credits--license) below.

## Introduction

A SSH Agent based-on Windows CryptoAPI.

This project allows other programs to access SSH keys stored in your Windows Certificate Store for authentication.

Benefit by Windows Certificate Management, this project natively supports the use of windows user certificates or smart cards, e.g., Yubikey PIV, for authentication.

## Overview
![Overview](overview.svg)

## Feature

* Work with smart cards natively without installing any driver in Windows (PIV only)
* Support for OpenSSH certificates (so you can use your smart card with an additional OpenSSH certificate)
* Good compatibility
* **New in KeyBridge:** built-in named-pipe relay (`keybridge relay ...`) for bridging any Windows named pipe into WSL — no separate `npiperelay.exe` needed

## Compatibility

There are many different OpenSSH agent implementations in Windows. This project implements five popular protocols in Windows:

* Cygwin UNIX Socket
* Windows UNIX Socket (Windows 10 1803 or later)
* Named pipe
* Pageant SSH Agent Protocol
* XShell Xagent Protocol

With the support of these protocols, this project is compatible with most SSH clients in Windows. For example:

* Git for Windows
* Windows Subsystem for Linux
* Windows OpenSSH
* Putty
* Jetbrains
* SecureCRT
* XShell
* Cygwin
* MINGW
* ...

## Installing

Build from source (`go build .` on Windows, or cross-compile with `GOOS=windows GOARCH=amd64 go build .`), or grab a build from the releases page once available.

You may make a shortcut of this application to the startup folder so it launches automatically.

## Usage

### Basic Usage (SSH agent)

1. Start `keybridge.exe`
2. Right-click the icon on your taskbar
3. You can get necessary information by selecting your interesting item in the menu

Note: Some SSH clients using Pageant Protocol, e.g., Putty, XShell and Jetbrains, needn't any setting in system wide, thus you can't see Pageant in the menu.

Check [Yubikey with WSL tutorial](doc/wsl_tutorial.md) to start using Yubikey with SSH on WSL.

### Relay mode (new)

```
keybridge.exe relay [-p] [-s] [-ep] [-ei] [-v] <named pipe path>
```

| Flag | Meaning |
|------|---------|
| `-p`  | poll until the named pipe exists |
| `-s`  | send a 0-byte message to the pipe after EOF on stdin |
| `-ep` | terminate on EOF reading from the pipe, even if there's more to write |
| `-ei` | terminate on EOF reading from stdin, even if there's more to write |
| `-v`  | verbose output on stderr |

Same flags as `npiperelay`, so any `socat ... EXEC:"npiperelay.exe ..."` command works unchanged with `EXEC:"keybridge.exe relay ..."`. To reach this binary's own SSH-agent pipe from WSL:

```bash
export SSH_AUTH_SOCK=$HOME/.ssh/agent.sock
socat UNIX-LISTEN:$SSH_AUTH_SOCK,fork EXEC:"keybridge.exe relay //./pipe/openssh-ssh-agent" &
```

(adjust the pipe path to whatever this binary is actually listening on — check the tray menu's Named Pipe entry for the exact path.)

### Work with Xshell

1. Install and run `keybridge.exe`
2. Open the Properties dialog box of your session.
3. From Category, select 'SSH', Select 'Use Xagent (SSH agent)' for passphrase handling.
4. From Category, select 'Authentication' and select 'Public Key' as the authentication method.

### OpenSSH Certificates

OpenSSH supports authentication using SSH certificates. Certificates contain a public key, identity information and are signed with a standard SSH key.

Unlike TLS using X.509, OpenSSH uses a special certificate format, thus we can't convert your X.509 certificate into OpenSSH format.

To deal with OpenSSH Certificates, this project introduces a public key override mechanism.

If you want to work with OpenSSH certificates, you should put your OpenSSH Certificates in your `user profile` folder, rename them to `<Your Certificate Common Name>-cert.pub` or `<Your Certificate Serial Number>-cert.pub`.

### Debug log

1. Run `setx WCSA_DEBUG 1`
2. Reboot to take effect
3. Reproduce your problem
4. The debug log is located in `%USERPROFILE%\WCSA_DEBUG.log`

## Credits & License

KeyBridge is a fork of [buptczq/WinCryptSSHAgent](https://github.com/buptczq/WinCryptSSHAgent), licensed under the [Apache License 2.0](LICENSE) (Copyright 2019 BUPTCZQ). All SSH-agent, CryptoAPI/CNG, and tray-app code is theirs, unmodified except where noted (see `main.go`).

The `relay` subcommand replicates the behavior (and CLI flags) of [jstarks/npiperelay](https://github.com/jstarks/npiperelay) (MIT License, © 2017 John Starks) as a design reference, but is an independent implementation written against `go-winio` rather than a copy of its code.
