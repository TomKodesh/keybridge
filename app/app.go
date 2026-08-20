package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

const (
	WSL_SOCK    = "keybridge-wsl.sock"
	CYGWIN_SOCK = "keybridge-cygwin.sock"
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

// ctxKey namespaces the values main.go stores on the context (the shared
// agent, the hv-client flag) so they can't collide with a string key some
// other package happens to also use.
type ctxKey int

const (
	CtxKeyAgent ctxKey = iota
	CtxKeyHV
)

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

// dataDir returns KeyBridge's per-user app-data directory
// (%LOCALAPPDATA%\KeyBridge on Windows), creating it if it doesn't exist
// yet. Socket files live here rather than in the home directory root so
// they don't clutter a folder the user browses directly.
func dataDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "KeyBridge")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}
