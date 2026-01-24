//go:build windows

package conpty

import (
	"context"
	"fmt"
	"os"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32                        = windows.NewLazySystemDLL("kernel32.dll")
	fCreatePseudoConsole               = modKernel32.NewProc("CreatePseudoConsole")
	fResizePseudoConsole               = modKernel32.NewProc("ResizePseudoConsole")
	fClosePseudoConsole                = modKernel32.NewProc("ClosePseudoConsole")
	fInitializeProcThreadAttributeList = modKernel32.NewProc("InitializeProcThreadAttributeList")
	fUpdateProcThreadAttribute         = modKernel32.NewProc("UpdateProcThreadAttribute")
)

const (
	_STILL_ACTIVE                        uint32  = 259
	_S_OK                                uintptr = 0
	_PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE uintptr = 0x20016
	_PSEUDOCONSOLE_INHERIT_CURSOR        uint32  = 0x1
)

type _COORD struct {
	X, Y int16
}

func (c *_COORD) Pack() uintptr {
	return uintptr((int32(c.Y) << 16) | int32(c.X))
}

type _HPCON windows.Handle

type handleIO struct {
	handle windows.Handle
}

func (h *handleIO) Read(p []byte) (int, error) {
	var numRead uint32 = 0
	err := windows.ReadFile(h.handle, p, &numRead, nil)
	return int(numRead), err
}

func (h *handleIO) Write(p []byte) (int, error) {
	var numWritten uint32 = 0
	err := windows.WriteFile(h.handle, p, &numWritten, nil)
	return int(numWritten), err
}

func (h *handleIO) Close() error {
	return windows.CloseHandle(h.handle)
}

// LocalConPty implements a local version of ConPty
type LocalConPty struct {
	hpc                          _HPCON
	pi                           *windows.ProcessInformation
	ptyIn, ptyOut, cmdIn, cmdOut *handleIO
	restoreMode                  func()
}

func win32ClosePseudoConsole(hPc _HPCON) {
	if fClosePseudoConsole.Find() != nil {
		return
	}
	fClosePseudoConsole.Call(uintptr(hPc))
}

func win32ResizePseudoConsole(hPc _HPCON, coord *_COORD) error {
	if fResizePseudoConsole.Find() != nil {
		return fmt.Errorf("ResizePseudoConsole not found")
	}
	ret, _, _ := fResizePseudoConsole.Call(uintptr(hPc), coord.Pack())
	if ret != _S_OK {
		return fmt.Errorf("ResizePseudoConsole failed with status 0x%x", ret)
	}
	return nil
}

func win32CreatePseudoConsole(c *_COORD, hIn, hOut windows.Handle, flags uint32) (_HPCON, error) {
	if fCreatePseudoConsole.Find() != nil {
		return 0, fmt.Errorf("CreatePseudoConsole not found")
	}
	var hPc _HPCON
	ret, _, _ := fCreatePseudoConsole.Call(
		c.Pack(),
		uintptr(hIn),
		uintptr(hOut),
		uintptr(flags),
		uintptr(unsafe.Pointer(&hPc)))
	if ret != _S_OK {
		return 0, fmt.Errorf("CreatePseudoConsole() failed with status 0x%x", ret)
	}
	return hPc, nil
}

type _StartupInfoEx struct {
	startupInfo   windows.StartupInfo
	attributeList []byte
}

func getStartupInfoExForPTY(hpc _HPCON) (*_StartupInfoEx, error) {
	if fInitializeProcThreadAttributeList.Find() != nil {
		return nil, fmt.Errorf("InitializeProcThreadAttributeList not found")
	}
	if fUpdateProcThreadAttribute.Find() != nil {
		return nil, fmt.Errorf("UpdateProcThreadAttribute not found")
	}
	var siEx _StartupInfoEx
	siEx.startupInfo.Cb = uint32(unsafe.Sizeof(windows.StartupInfo{}) + unsafe.Sizeof(&siEx.attributeList[0]))
	siEx.startupInfo.Flags |= windows.STARTF_USESTDHANDLES
	var size uintptr

	// first call is to get required size. this should return false.
	fInitializeProcThreadAttributeList.Call(0, 1, 0, uintptr(unsafe.Pointer(&size)))
	siEx.attributeList = make([]byte, int(size))
	ret, _, err := fInitializeProcThreadAttributeList.Call(
		uintptr(unsafe.Pointer(&siEx.attributeList[0])),
		1,
		0,
		uintptr(unsafe.Pointer(&size)))
	if ret != 1 {
		return nil, fmt.Errorf("InitializeProcThreadAttributeList: %v", err)
	}

	ret, _, err = fUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(&siEx.attributeList[0])),
		0,
		_PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(hpc),
		unsafe.Sizeof(hpc),
		0,
		0)
	if ret != 1 {
		return nil, fmt.Errorf("UpdateProcThreadAttribute: %v", err)
	}
	return &siEx, nil
}

func createConsoleProcessAttachedToPTY(hpc _HPCON, commandLine, workDir string, env []string) (*windows.ProcessInformation, error) {
	cmdLine, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}
	var currentDirectory *uint16
	if workDir != "" {
		currentDirectory, err = windows.UTF16PtrFromString(workDir)
		if err != nil {
			return nil, err
		}
	}
	var envBlock *uint16
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT)
	if env != nil {
		flags |= uint32(windows.CREATE_UNICODE_ENVIRONMENT)
		envBlock = createEnvBlock(env)
	}
	siEx, err := getStartupInfoExForPTY(hpc)
	if err != nil {
		return nil, err
	}
	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		nil, // use this if no args
		cmdLine,
		nil,
		nil,
		false, // inheritHandle
		flags,
		envBlock,
		currentDirectory,
		&siEx.startupInfo,
		&pi)
	if err != nil {
		return nil, err
	}
	return &pi, nil
}

func createEnvBlock(envv []string) *uint16 {
	if len(envv) == 0 {
		return &utf16.Encode([]rune("\x00\x00"))[0]
	}
	length := 0
	for _, s := range envv {
		length += len(s) + 1
	}
	length += 1

	b := make([]byte, length)
	i := 0
	for _, s := range envv {
		l := len(s)
		copy(b[i:i+l], []byte(s))
		copy(b[i+l:i+l+1], []byte{0})
		i = i + l + 1
	}
	copy(b[i:i+1], []byte{0})

	return &utf16.Encode([]rune(string(b)))[0]
}

func closeHandles(handles ...windows.Handle) error {
	var err error
	for _, h := range handles {
		if h != windows.InvalidHandle {
			if e := windows.CloseHandle(h); e != nil && err == nil {
				err = e
			}
		}
	}
	return err
}

func (cpty *LocalConPty) Close() error {
	if cpty.restoreMode != nil {
		cpty.restoreMode()
	}
	win32ClosePseudoConsole(cpty.hpc)
	return closeHandles(
		cpty.pi.Process,
		cpty.pi.Thread,
		cpty.ptyIn.handle,
		cpty.ptyOut.handle,
		cpty.cmdIn.handle,
		cpty.cmdOut.handle)
}

func (cpty *LocalConPty) Wait(ctx context.Context) (uint32, error) {
	var exitCode uint32 = _STILL_ACTIVE
	for {
		if err := ctx.Err(); err != nil {
			return _STILL_ACTIVE, fmt.Errorf("wait canceled: %v", err)
		}
		ret, _ := windows.WaitForSingleObject(cpty.pi.Process, 1000)
		if ret != uint32(windows.WAIT_TIMEOUT) {
			err := windows.GetExitCodeProcess(cpty.pi.Process, &exitCode)
			return exitCode, err
		}
	}
}

func (cpty *LocalConPty) Resize(width, height int) error {
	coords := _COORD{
		int16(width),
		int16(height),
	}
	return win32ResizePseudoConsole(cpty.hpc, &coords)
}

func (cpty *LocalConPty) Read(p []byte) (int, error) {
	return cpty.cmdOut.Read(p)
}

func (cpty *LocalConPty) Write(p []byte) (int, error) {
	return cpty.cmdIn.Write(p)
}

// IsConPtyAvailable checks if the necessary ConPTY APIs are available on the system
func IsConPtyAvailable() bool {
	// Find will return nil if the function is found and an error if it is not found
	return fCreatePseudoConsole.Find() == nil &&
		fResizePseudoConsole.Find() == nil &&
		fClosePseudoConsole.Find() == nil &&
		fInitializeProcThreadAttributeList.Find() == nil &&
		fUpdateProcThreadAttribute.Find() == nil
}

// startOptions holds configuration for starting ConPTY
type startOptions struct {
	flags      uint32
	rawConsole bool
}

// ConPtyOption defines a functional option for configuring ConPTY
type ConPtyOption func(*startOptions)

// WithInheritCursor configures whether the pseudo console should inherit the cursor position.
func WithInheritCursor(inherit bool) ConPtyOption {
	return func(opts *startOptions) {
		if inherit {
			opts.flags |= _PSEUDOCONSOLE_INHERIT_CURSOR
		} else {
			opts.flags &^= _PSEUDOCONSOLE_INHERIT_CURSOR
		}
	}
}

// WithConsoleModeRaw configures the specific console handles to be in raw mode and enables VT processing
func WithConsoleModeRaw() ConPtyOption {
	return func(opts *startOptions) {
		opts.rawConsole = true
	}
}

// StartConPty starts a new process attached to a pseudo console with specific options
func StartConPty(commandLine string, width, height int, env []string, options ...ConPtyOption) (*LocalConPty, error) {
	if !IsConPtyAvailable() {
		return nil, fmt.Errorf("ConPty is not available")
	}

	opts := &startOptions{}
	for _, opt := range options {
		opt(opts)
	}

	coords := _COORD{int16(width), int16(height)}

	var cmdIn, cmdOut, ptyIn, ptyOut windows.Handle
	if err := windows.CreatePipe(&ptyIn, &cmdIn, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe: %v", err)
	}
	if err := windows.CreatePipe(&cmdOut, &ptyOut, nil, 0); err != nil {
		closeHandles(ptyIn, cmdIn)
		return nil, fmt.Errorf("CreatePipe: %v", err)
	}

	hPc, err := win32CreatePseudoConsole(&coords, ptyIn, ptyOut, opts.flags)
	if err != nil {
		// If PSEUDOCONSOLE_INHERIT_CURSOR flag is set and creation failed,
		// try again without it as a fallback.
		// Both CreatePseudoConsole and PSEUDOCONSOLE_INHERIT_CURSOR were
		// introduced in Windows 10 version 1809 (October 2018 Update), so there
		// is no particular reason to believe that would fail for the cursor inheritance,
		// but just in case there's some API discrepancy of some short.
		if opts.flags&_PSEUDOCONSOLE_INHERIT_CURSOR != 0 {
			hPc, err = win32CreatePseudoConsole(&coords, ptyIn, ptyOut, opts.flags&^_PSEUDOCONSOLE_INHERIT_CURSOR)
		}

		// If it still fails, return the error
		if err != nil {
			closeHandles(ptyIn, ptyOut, cmdIn, cmdOut)
			return nil, err
		}
	}

	pi, err := createConsoleProcessAttachedToPTY(hPc, commandLine, "", env)
	if err != nil {
		closeHandles(ptyIn, ptyOut, cmdIn, cmdOut)
		win32ClosePseudoConsole(hPc)
		return nil, fmt.Errorf("Failed to create console process: %v", err)
	}

	var restoreMode func()
	if opts.rawConsole {
		var err error
		restoreMode, err = enableRawMode()
		if err != nil {
			closeHandles(ptyIn, ptyOut, cmdIn, cmdOut)
			win32ClosePseudoConsole(hPc)
			return nil, fmt.Errorf("failed to enable raw mode: %v", err)
		}
	}

	cpty := &LocalConPty{
		hpc:         hPc,
		pi:          pi,
		ptyIn:       &handleIO{ptyIn},
		ptyOut:      &handleIO{ptyOut},
		cmdIn:       &handleIO{cmdIn},
		cmdOut:      &handleIO{cmdOut},
		restoreMode: restoreMode,
	}
	return cpty, nil
}

func enableRawMode() (func(), error) {
	in := windows.Handle(os.Stdin.Fd())
	out := windows.Handle(os.Stdout.Fd())

	var inMode, outMode uint32
	if err := windows.GetConsoleMode(in, &inMode); err != nil {
		return nil, err
	}
	if err := windows.GetConsoleMode(out, &outMode); err != nil {
		return nil, err
	}

	newInMode := inMode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT)
	newInMode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT

	newOutMode := outMode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING

	if err := windows.SetConsoleMode(in, newInMode); err != nil {
		return nil, err
	}
	if err := windows.SetConsoleMode(out, newOutMode); err != nil {
		// Restore input mode if output mode fails
		_ = windows.SetConsoleMode(in, inMode)
		return nil, err
	}

	return func() {
		_ = windows.SetConsoleMode(in, inMode)
		_ = windows.SetConsoleMode(out, outMode)
	}, nil
}
