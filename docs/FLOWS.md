# KeyBridge — Application Flows

This document maps every code path in KeyBridge: how the process decides what to run at startup, how each of the seven transports feeds into the same SSH-agent core, how a sign request actually reaches the smart card and comes back, and how the new relay subcommand behaves internally. It's meant to be read alongside the source, not instead of it — each diagram names the actual file/function it corresponds to.

## 1. Startup and mode selection

Everything starts in `main()`. The very first check — added for KeyBridge — decides whether this run is the relay tool or the SSH agent; everything below it is unmodified upstream behavior.

```mermaid
flowchart TD
    Start(["Process starts"]) --> ArgCheck{"os.Args[1] == relay?"}
    ArgCheck -- yes --> Relay["runRelay (relay.go)\nnamed-pipe <-> stdio bridge"]
    Relay --> RelayEnd(["exit"])

    ArgCheck -- no --> ParseFlags["flag.Parse()\n-i / -disable-capi / -disable-pin-cache"]
    ParseFlags --> DPI["SetProcessSystemDpiAware()"]
    DPI --> DebugLog{"WCSA_DEBUG=1?"}
    DebugLog -- yes --> RedirectLog["redirect stdout/stderr\nto %USERPROFILE%\\WCSA_DEBUG.log"]
    DebugLog -- no --> InstallCheck
    RedirectLog --> InstallCheck{"-i flag set?"}
    InstallCheck -- yes --> Elevate["installService():\nregister Hyper-V Guest Communication\nService (elevates via UAC if needed)"]
    Elevate --> InstallEnd(["exit"])
    InstallCheck -- no --> HVDetect["ConnectHyperV():\nis this process itself running\ninside a Hyper-V guest VM?"]
    HVDetect --> AgentChoice{"choose agent backend"}
    AgentChoice -- "yes, I'm a guest VM" --> HVAgentBox["HVAgent\n(forwards every request to\nthe host over a vsock)"]
    AgentChoice -- "-disable-capi" --> KeyRingOnly["KeyRingAgent only\n(in-memory keys, ssh-add style)"]
    AgentChoice -- "default: I'm the host" --> Wrapped["WrappedAgent =\nKeyRingAgent + CAPIAgent"]
    HVAgentBox --> Transports
    KeyRingOnly --> Transports
    Wrapped --> Transports
    Transports["start all 7 Application listeners\n(PubKeyView, WSL, VSock, Cygwin,\nNamedPipe, Pageant, XShell)"]
    Transports --> Tray["show system tray icon + menu"]
    Tray --> Loop["event loop:\nmenu clicks, balloon events, quit signal"]
    Loop -- quit --> Shutdown["cancel context;\nwait up to 5s for listeners to close"]
```

**Two things worth calling out:**
- The `-i` install path and the relay path both exit before the tray/agent ever starts — they're one-shot operations, not the normal running mode.
- "Is this process a Hyper-V guest" and "is this process serving Hyper-V guests" are *opposite roles* using the *same* socket mechanism — see §5.

## 2. How the six network transports share one agent core

KeyBridge speaks six different wire protocols (five upstream, all unchanged), but every one of them ends up calling the exact same handler. This is why the relay addition was safe to make: it only had to reach the *NamedPipe* transport's existing pipe, not reimplement anything about how requests are served.

```mermaid
flowchart LR
    subgraph Transports["app/*.go — one goroutine loop per transport"]
        NP["NamedPipe\n\\\\.\\pipe\\openssh-ssh-agent"]
        PG["Pageant\nhidden window + WM_COPYDATA"]
        CY["Cygwin\nfake AF_UNIX file + TCP + UUID handshake"]
        WS["WSL\nWin10 AF_UNIX socket, or TCP fallback"]
        VS["VSock / Hyper-V\nWSL2 vsock, dynamic per-VM workers"]
        XS["XShell\nXagent protocol over TCP"]
    end
    NP --> Handler
    PG --> Handler
    CY --> Handler
    WS --> Handler
    VS --> Handler
    XS --> Handler
    Handler["Server.SSHAgentHandler\n(sshagent/server.go)"] --> ServeAgent["agent.ServeAgent\n(golang.org/x/crypto/ssh/agent)\nstandard SSH agent wire protocol"]
    ServeAgent --> AgentIface["the one configured agent.Agent\n(WrappedAgent / KeyRingAgent-only / HVAgent)"]
```

`PubKeyView` (the "Show Public Keys" tray menu item) isn't shown above — it doesn't listen on anything, it just holds a reference to the agent for the menu's `List()` call.

## 3. A sign request, end to end (the flagship path)

This is the flow that matters most: a WSL process asking to sign an SSH auth challenge, and the request actually reaching the PIV card. Every hop after "Relay" is completely unmodified upstream code — the relay only had to get bytes to the pipe.

```mermaid
sequenceDiagram
    participant WSL as WSL process (ssh, oc, aws, ...)
    participant Socat as socat (WSL)
    participant Relay as keybridge.exe relay (Windows, via WSL interop)
    participant Pipe as Named pipe \\.\pipe\openssh-ssh-agent
    participant Agent as keybridge.exe (agent process)
    participant Wrapped as WrappedAgent
    participant CAPI as CAPIAgent
    participant Win as Windows CryptoAPI / CNG
    participant Card as PIV smart card / cert store

    WSL->>Socat: connect to $SSH_AUTH_SOCK
    Socat->>Relay: spawn "keybridge.exe relay ..." per connection
    Relay->>Pipe: DialPipe (retries every 200ms if -p set)
    Pipe->>Agent: pipe.Accept() -> new connection
    Agent->>Wrapped: agent.ServeAgent(conn)
    WSL->>Socat: SSH_AGENTC_SIGN_REQUEST
    Socat->>Relay: bytes over stdin
    Relay->>Pipe: io.Copy(pipe, stdin)
    Pipe->>Wrapped: SignWithFlags(key, data, flags)
    Wrapped->>CAPI: try CAPIAgent (KeyRingAgent has no matching key)
    CAPI->>Win: CryptAcquireCertificatePrivateKey
    CAPI->>Win: CryptSignMessage
    Win->>Card: sign challenge (PIN / touch prompt here)
    Card-->>Win: raw signature
    Win-->>CAPI: PKCS7 signed blob
    CAPI-->>Wrapped: ssh.Signature
    Wrapped-->>Pipe: SSH_AGENT_SIGN_RESPONSE
    Pipe-->>Relay: io.Copy(stdout, pipe)
    Relay-->>Socat: bytes over stdout
    Socat-->>WSL: signature delivered
    WSL->>WSL: completes SSH auth to remote server
```

If `WrappedAgent` had a matching key in `KeyRingAgent` instead (a plain key added via `ssh-add`), the CryptoAPI/PIV hop is skipped entirely — `WrappedAgent.SignWithFlags` tries each wrapped agent in order and returns on the first success.

## 4. Relay mode internals (`relay.go`)

Two things run concurrently: the goroutine copying stdin into the pipe, and the main goroutine copying the pipe to stdout. The four flags (`-p`, `-s`, `-ep`, `-ei`) all just change exit/wait behavior around the edges of this shape.

```mermaid
flowchart TD
    A["runRelay(args)"] --> B["dialPipeWithPoll:\nretry every 200ms if -p set\nand pipe doesn't exist yet"]
    B --> C{"connected?"}
    C -- "no, not polling" --> Z1(["log.Fatalln, exit 1"])
    C -- yes --> D["spawn goroutine: stdin -> pipe"]
    C -- yes --> E["main goroutine: pipe -> stdout"]

    subgraph G1["goroutine: stdin -> pipe"]
        D --> D1["io.Copy(pipe, stdin)"]
        D1 --> D2{"error?"}
        D2 -- yes --> Z2(["log.Fatalln, exit 1"])
        D2 -- "no, clean EOF" --> D3{"-ei set?"}
        D3 -- yes --> Z3(["os.Exit(0) immediately"])
        D3 -- no --> D4{"-s set?"}
        D4 -- yes --> D5["write a zero-length message\n(end-of-data marker on\na message-mode pipe)"]
        D4 -- no --> D6
        D5 --> D6["close(stdinDone) channel"]
    end

    subgraph G2["main goroutine: pipe -> stdout"]
        E --> E1{"error?"}
        E1 -- yes --> Z4(["log.Fatalln, exit 1"])
        E1 -- "no, EOF\n(go-winio maps a broken pipe to\nio.EOF too -- can't tell them apart)" --> E2{"-ep set?"}
        E2 -- yes --> Z5(["os.Exit(0) immediately,\ndon't wait for the stdin side"])
        E2 -- no --> E3["close stdout"]
        E3 --> E4["block on <-stdinDone"]
    end

    D6 -.unblocks.-> E4
    E4 --> Done(["process exits normally"])
```

The go-winio note in `E1` is the one meaningful behavioral difference from upstream npiperelay (documented in the source comment too): npiperelay's raw Win32 calls can distinguish "the remote signaled a clean end-of-data" from "the pipe actually broke," and exits immediately without waiting on stdin for the latter. go-winio surfaces both as plain `io.EOF`, so KeyBridge can't make that distinction here — it always waits on the stdin side to finish on its own instead (or exits immediately if `-ep` is set, same as before).

## 5. WSL2 / Hyper-V vsock — two opposite roles, one mechanism

This is the part of the codebase most likely to be misread, because the same `winio.ListenHvsock` / vsock machinery is used for two different roles depending on which side of the VM boundary this process is running on.

```mermaid
flowchart TD
    Q{"Is this KeyBridge process\nrunning inside a Hyper-V guest VM?"}
    Q -- "yes (rare — e.g. a dev VM)" --> R["HVAgent (sshagent/hyperv.go)\nevery List/Sign call opens a NEW\nvsock connection to the host and\nproxies the request there"]
    Q -- "no — this is the normal case:\nrunning on the physical host" --> S["VSock.Run (app/vsock.go)\nlistens for WSL2 guests connecting IN"]
```

```mermaid
flowchart TD
    Start(["VSock.Run, host mode"]) --> Check1{"CheckHvSocket()?"}
    Check1 -- no --> NoOp1(["return nil — Hyper-V sockets\nnot available on this system"])
    Check1 -- yes --> Check2{"CheckHVService() installed?"}
    Check2 -- no --> NoOp2(["return nil — user must run\nwith -i once, as admin, then reboot"])
    Check2 -- yes --> ListenWild["ListenHvsock on the wildcard VMID\n(catches VMs already running)"]
    ListenWild --> Watcher["spawn wsl2Watcher goroutine"]

    subgraph WatcherBox["wsl2Watcher (background, runs for the process lifetime)"]
        W1["watch for wslhost.exe process\nlaunches/exits (ProcessNotify),\nfalls back to 15s polling if that fails"]
        W1 --> W2["GetVMIDs(): enumerate currently\nrunning WSL2 VM instances"]
        W2 --> W3["diff against the last known list"]
        W3 --> W4["new VM found -> open a\nper-VM Hyper-V socket worker"]
        W3 --> W5["VM gone -> close and remove\nits worker"]
        W4 --> W1
        W5 --> W1
    end

    ListenWild --> AcceptLoop["accept loop on the wildcard listener"]
    AcceptLoop --> Handler2["-> Server.SSHAgentHandler\n(same handler as every other transport)"]
```

KeyBridge's relay mode is an alternative to this whole mechanism for reaching WSL, not a replacement for it — the vsock path needs a one-time elevated install (`-i`) and a reboot; the relay path only needs `socat` and no elevation at all. Which one is actually in use is a per-environment choice.

## 6. Loading identities: cert store, EKU filtering, and OpenSSH certificate pairing

This runs once per `List()` call (`CAPIAgent.loadCerts()`), which happens on every `SSH_AGENTC_REQUEST_IDENTITIES` — so certificate changes (a new smart card inserted, a cert renewed) are picked up live, no restart needed.

```mermaid
flowchart TD
    A["CAPIAgent.loadCerts()"] --> B["capi.LoadUserCerts():\nenumerate CurrentUser \\ My cert store"]
    B --> C{"has a usable private key?"}
    C -- no --> Skip1(["skip"])
    C -- yes --> D["FilterCertificateEKU:\nAny / Client Auth / Smart Card Logon -> allow\nBitLocker / Server Auth only -> reject"]
    D -- rejected --> Skip2(["skip, free the cert handle"])
    D -- allowed --> E["build an ssh.PublicKey from the\nX.509 public key (RSA or ECDSA)"]
    E --> F["wrap in rsaSigner / ecdsaSigner\n(defers actual signing to capi.Sign)"]
    F --> G["register as a plain SSH identity"]
    G --> H{"matching <serial>-cert.pub or\n<CommonName>-cert.pub in the\nuser profile folder?"}
    H -- no --> I(["done with this certificate"])
    H -- yes --> J["ssh.NewCertSigner:\nwrap the SAME CAPI signer under\nthe OpenSSH certificate"]
    J --> K["register a SECOND identity\n(certificate-backed, same key,\nsame PIV signing underneath)"]
    K --> I
```

## 7. Quick reference: which flow applies when

| Scenario | Diagram | Notes |
|---|---|---|
| `keybridge.exe relay //./pipe/...` invoked from WSL | §4, then §3 from "Pipe" onward | The normal KeyBridge-from-WSL path |
| Native Windows SSH client (PuTTY, Git for Windows, JetBrains) | §3, starting from whichever transport it uses instead of the relay | Same agent core, different transport |
| First-time smart card setup / renewed certificate | §6 | Runs automatically on the next `List()`, no restart |
| `-i` flag | §1 (Install path) | One-shot, elevates, requires reboot to take effect |
| Running inside a Hyper-V dev VM | §5, `HVAgent` branch | Uncommon; most users are the host, not a guest |
| WSL2 via the built-in vsock mechanism (not the relay) | §5, `VSock.Run` branch | Alternative to the relay; needs `-i` + reboot once |
| `-disable-capi` | §1, `KeyRingOnly` branch | No smart card access at all — plain `ssh-add` keys only |
