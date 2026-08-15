package app

import (
	"context"
	"io"
)

// WSL_SOCK and CYGWIN_SOCK are on-disk socket filenames, not display
// strings -- intentionally left as-is rather than renamed to "keybridge-*"
// as part of the fork, so any external scripts/tooling written against the
// original file paths keep working unchanged.
const (
	WSL_SOCK    = "wincrypt-wsl.sock"
	CYGWIN_SOCK = "wincrypt-cygwin.sock"
	NAMED_PIPE  = "\\\\.\\pipe\\openssh-ssh-agent"
	APP_CYGWIN  = iota
	APP_WSL
	APP_WINSSH
	APP_HYPERV
	APP_SECURECRT
	APP_PAGEANT
	APP_XSHELL
	APP_PUBKEY
	APP_WSL2
	MENU_QUIT
)

type Application interface {
	AppId() AppId
	Run(ctx context.Context, handler func(conn io.ReadWriteCloser)) error
	Menu(func(id AppId, name string, handler func()))
}

type AppId int

var appIdToName = map[AppId]string{
	APP_CYGWIN:    "Cygwin",
	APP_WSL:       "WSL",
	APP_WINSSH:    "WinSSH",
	APP_SECURECRT: "SecureCRT",
	APP_PAGEANT:   "Pageant",
	APP_XSHELL:    "XShell",
	APP_HYPERV:    "Hyper-V",
}

var appIdToFullName = map[AppId]string{
	APP_CYGWIN:    "Cygwin (MinGW64 & MSYS2)",
	APP_WSL:       "Windows Subsystem for Linux",
	APP_WINSSH:    "Windows OpenSSH",
	APP_SECURECRT: "SecureCRT",
	APP_PAGEANT:   "Pageant",
	APP_XSHELL:    "XShell",
	APP_HYPERV:    "Hyper-V",
}

func (id AppId) String() string {
	return appIdToName[id]
}

func (id AppId) FullName() string {
	return appIdToFullName[id]
}
