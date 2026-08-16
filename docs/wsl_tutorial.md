# Yubikey on WSL

This tutorial will guide you to configure a YubiKey so it can be used with SSH under WSL. We will use the YubiKey as a PIV-compatible smart card. Note that YubiKey also has other modes that can be used for secure SSH access, like GPG, that are not covered in this tutorial.

## Prerequisites

* Fresh YubiKey 5
* Yubico software from https://www.yubico.com/products/services-software/download/smart-card-drivers-tools/
  * YubiKey Manager (graphic interface) - it also installs `ykman.exe`
  * YubiKey Smart Card Minidriver (Windows) - it is required to get ECDSA instead of default RSA
* KeyBridge (this repository) — build from source, or grab `keybridge.exe` from the [releases page](https://github.com/TomKodesh/keybridge/releases)
* Console (ie. `cmd.exe` or Windows Terminal)

## Steps

### Insert YubiKey into USB port of your computer

You can check with Device Manager (`devmgmt.msc`) that the system recognized your key. It will be listed under *Smart Cards* as *YubiKey Smart Card Minidriver*.

### Change default PIN and PUK

Execute following commands, provide new PIN and PUK when prompted:

1. `"C:\Program Files\Yubico\YubiKey Manager\ykman.exe" piv access set-retries 5 10`
1. `"C:\Program Files\Yubico\YubiKey Manager\ykman.exe" piv access change-pin --pin 123456`
1. `"C:\Program Files\Yubico\YubiKey Manager\ykman.exe" piv access change-puk --puk 12345678`
1. `"C:\Program Files\Yubico\YubiKey Manager\ykman.exe" piv access change-management-key --generate --protect --touch`

  This will give you a YubiKey with PIN and PUK that is only known to you and requires touch to change keys on it.

### Generate Keys

1. `"C:\Program Files\Yubico\YubiKey Manager\ykman.exe" piv keys generate --algorithm ECCP384 --format PEM --pin-policy ONCE --touch-policy ALWAYS 9a "%UserProfile%\Desktop\%username%_public_key.pem"`

    Command generates private key inside of YubiKey. It is not possible to extract it so it is very secure. Also it requires a touch every time it is used for authentication.

1. `"C:\Program Files\Yubico\YubiKey Manager\ykman.exe" piv certificates generate --valid-days 365 --subject "SSH Key" 9a "%UserProfile%\Desktop\%username%_public_key.pem"`

    Command generates a certificate from your public key. In brief: Windows needs it when speaking to your YubiKey.

### Check Windows Certificate Store

 1. Unplug your YubiKey.
 1. Plug your YubiKey back.
 1. Run Certificate Manager Tool (`certmgr.msc`) and in *Certificates - Current User \ Personal \ Certificates* your certificate named **SSH key** should be visible.

***NOTE:*** Please make sure [Allow ECC certificates to be used for logon and authentication](https://docs.microsoft.com/en-us/windows/security/identity-protection/smart-cards/smart-card-group-policy-and-registry-settings#allow-ecc-certificates-to-be-used-for-logon-and-authentication) in *Group Policy Editor (gpedit.msc) > Computer Configuration > Administrative Templates > Windows Components > Smart Card* is enabled.

***NOTE#2:*** You should also install the [YubiKey Smart Card Minidriver](https://www.yubico.com/support/download/smart-card-drivers-tools/) if you want to work with ECC algorithm certificates.

### Configure YubiKey for SSH in WSL and target machine

1. Ensure that `keybridge.exe` is running.
1. Run your WSL console and execute the command `which socat` to check if `socat` is present.

   *Some WSL Linux distros don't include `socat` by default, such as Ubuntu 20.04*

   a) If `socat` is not installed, install it before continuing. (Debian/Ubuntu example: `sudo apt install -y socat`)
1. Right-click KeyBridge's icon in the tray and select **Show WSL Settings** (or **Show WSL2 / Linux On Hyper-V Settings** if using WSL2 and/or Hyper-V) then press OK.

    A line like `export SSH_AUTH_SOCK=/mnt/c/Users/Jane/wincrypt-wsl.sock` will be copied into your clipboard for WSL — assuming your Windows build supports native AF_UNIX sockets (Win10 1803+). (The socket filename is `wincrypt-wsl.sock` regardless of the product rename — that's an internal artifact name the binary still writes, not a typo.) If it doesn't, KeyBridge falls back to a different mechanism entirely — a `socat`-bridged TCP listener at `/tmp/ssh-capi-agent.sock`, a different path with no "wincrypt" in the name at all. If what's actually in your clipboard doesn't match the block above, this fallback is almost certainly why — not a bug.

    For WSL2 / Hyper-V, lines like this will be copied into your clipboard:
    ```
    export SSH_AUTH_SOCK=/tmp/wincrypt-hv.sock
    ss -lnx | grep -q $SSH_AUTH_SOCK
    if [ $? -ne 0 ]; then
     rm -f $SSH_AUTH_SOCK
      (setsid nohup socat UNIX-LISTEN:$SSH_AUTH_SOCK,fork SOCKET-CONNECT:40:0:x0000x33332222x02000000x00000000 >/dev/null 2>&1) & disown
    fi
    ```

    This is KeyBridge's built-in WSL2 mechanism (§5 of [`FLOWS.md`](FLOWS.md)) — it needs the one-time elevated `-i` install step and a reboot. If you'd rather avoid that, KeyBridge's own [named-pipe relay](../README.md#using-the-named-pipe-relay) is a simpler alternative that needs no elevation at all.
1. Run your WSL console and execute the command from the previous step.
1. `ssh` into your target machine, authenticate with credentials used until now.
1. Right-click KeyBridge's icon in the tray and select **Show Public Keys** then press OK.

    All known keys in SSH format will be copied. You need to locate one named **SSH key**.

1. Copy the line with *SSH key* into `~\.ssh\authorized_keys` on the target machine.
1. Disconnect from the target machine.

### Use YubiKey for SSH

1. `ssh` into your machine.
1. Provide PIN when Windows asks.
1. Touch YubiKey twice (it should be blinking).
1. You should be allowed into your target machine. Enjoy! :rocket:
