//go:build windows

package conpty

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWithWorkDirOption(t *testing.T) {
	opts := &startOptions{}
	WithWorkDir(`C:\Windows\Temp`)(opts)
	if opts.workDir != `C:\Windows\Temp` {
		t.Fatalf("expected workDir %q, got %q", `C:\Windows\Temp`, opts.workDir)
	}

	WithWorkDir("")(opts)
	if opts.workDir != "" {
		t.Fatalf("expected empty workDir, got %q", opts.workDir)
	}
}

func TestStartConPtyWithWorkDir(t *testing.T) {
	if !IsConPtyAvailable() {
		t.Skip("ConPTY is not available on this system")
	}

	dir := t.TempDir()
	cpty, err := StartConPty("cmd.exe /c cd", 80, 25, os.Environ(), WithWorkDir(dir))
	if err != nil {
		t.Fatalf("StartConPty failed: %v", err)
	}
	defer cpty.Close()

	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, cpty)
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exitCode, err := cpty.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (output: %q)", exitCode, out.String())
	}

	_ = cpty.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	if !strings.Contains(strings.ToLower(out.String()), strings.ToLower(dir)) {
		t.Fatalf("expected output to contain working directory %q, got %q", dir, out.String())
	}
}
