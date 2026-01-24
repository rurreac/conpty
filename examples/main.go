//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/rurrea/conpty"
)

func main() {
	inheritCursor := flag.Bool("inherit-cursor", false, "Enable cursor inheritance (keeps cursor position on start)")
	flag.Parse()

	shouldInherit := *inheritCursor
	options := []conpty.ConPtyOption{
		conpty.WithInheritCursor(shouldInherit),
		conpty.WithConsoleModeRaw(),
	}

	// Start ConPTY with Windows Command Prompt
	cpty, err := conpty.StartConPty("cmd.exe", 80, 25, os.Environ(), options...)
	if err != nil {
		log.Fatalf("Failed to spawn conpty: %v", err)
	}
	defer cpty.Close()

	fmt.Printf("Spawned cmd.exe (Inherit Cursor: %v)\r\n", shouldInherit)

	// Copy I/O
	go func() {
		_, _ = io.Copy(cpty, os.Stdin)
	}()

	go func() {
		_, _ = io.Copy(os.Stdout, cpty)
	}()

	// Wait for the process to exit
	exitCode, err := cpty.Wait(context.Background())
	if err != nil {
		log.Printf("Wait error: %v", err)
	}
	fmt.Printf("\nProcess exited with code %d\r\n", exitCode)
}
