@echo off
setlocal

@rem Build all by default
if [%1]==[] (
	go generate || exit /b 1
	call :build 386 KeyBridge_32bit.exe
	call :build amd64 KeyBridge.exe
) else (
	go generate || exit /b 1
	call :build %1 KeyBridge-%1.exe
)
goto :eof


:build
set arch=%1
set output=%2
echo Build %arch% to %output%

set GOARCH=%arch%
go build -ldflags "-w -s -H=windowsgui" -trimpath -o %output% || exit /b 1

goto :eof
