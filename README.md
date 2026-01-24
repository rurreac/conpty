# conpty

Go implementation of Windows Pseudo Console ([ConPTY](https://devblogs.microsoft.com/commandline/windows-command-line-introducing-the-windows-pseudo-console-conpty/)) 

## Why this implementation?
By default standard ConPTY implementations will perform a standard pseudoconsole creation which can be extremly disrrupting if you intend to integrate it with your own terminal emulator.

This implementation allows enabling `PSEUDOCONSOLE_INHERIT_CURSOR` as an option to prevent in particular initial screen clearing and other sequences that are only meant for Windows.
It also lets you decide if you want ConPTY in raw mode or not. Generally speaking, you might always want to set it to raw mode, but it might be useful depending on your particular use case.

### The issue with the standard pseudoconsole creation
Let's say that you intend to receive the output of a shell using ConPTY having created the pseudo console with the default settings. Then you will receive [the following escape sequences](#escape-sequences-sent-by-conpty-at-initialization-standard-pseudoconsole-creation) before the shell prompt. If you receive this output, for instance, over the network (e.g. SSH) and the remote client is running on Unix-like systems (SSH client on linux) it will interpret these sequences.

The combination of `\x1b[2J` and `\x1b[H` will clear the screen of the remote client. While a Windows handles this transparently (visually, it will scroll up what's in the terminal and position the cursor at the top-left corner), a Unix-like client will create a page of blank lines and position the cursor at the top-left corner.

Continuing with our SSH example, imagine an SSH client that after connecting you with a remote server, prints a page of blank lines positions the cursor at the top-left corner and then shows the prompt. Although the banner will be seen in cursor position (1,1), the blank lines will create a gap between the banner of the remote server and the SSH command, which is not really what you want.

### Escape sequences sent by ConPTY at initialization (standard pseudoconsole creation)
Considering you spawn a shell using ConPTY, it will send the following escape sequences (in this order) before the banner appears: 

1.  `\x1b[?9001h` - Enables Win32 Input Mode.
This is a Microsoft-specific private sequence used by the Windows Console (ConPTY). When enabled, the terminal sends input events (like mouse clicks or complex key combinations) serialized as escape sequences that map directly to Windows INPUT_RECORD structures. This allows applications to receive high-fidelity Windows input events over a text stream (SSH/PTY).
Counterspart: `\x1b[?9001l` disables Win32 Input Mode (sent on exit).

2. `\x1b[?1004h` - Enables Focus Reporting.
When enabled, the terminal will send a specific escape sequence to the application whenever the terminal window gains or loses focus.
Gains focus: sends `\x1b[I`
Loses focus: sends `\x1b[O`
Counterpart: `\x1b[?1004l` disables Focus Reporting (sent on exit).

3. `\x1b[?25l` - Hides the Cursor.
`?25`: Refers to the cursor visibility parameter (DECTCEM).
`l`: Stands for "Low" or "Reset" (Disable).
Counterpart: `\x1b[?25h` shows the cursor).

4. `\x1b[2J` - Clears the Entire Screen.
`J`: Erase in Display (ED).
`2`: Argument specifying "entire screen".

5. `\x1b[m` - Resets SGR (Select Graphic Rendition).
This effectively turns off all text attributes (bold, underline, etc.) and resets foreground/background colors to the terminal's default (equivalent to `\x1b[0m`).

6. `\x1b[H` - Moves the cursor to the top-left corner of the screen (row 1, column 1).

NB: \x1b (hex) or 27 (decimal) stands for the escape character 

## Usage Example

```go
package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/rurrea/conpty"
)

func main() {
	cpty, err := conpty.StartConPty("cmd.exe", 80, 25, os.Environ(), conpty.WithInheritCursor(true), conpty.WithConsoleModeRaw())
	if err != nil {
		log.Fatalf("Failed to spawn conpty: %v", err)
	}
	defer cpty.Close()

	go func() {
		_, _ = io.Copy(cpty, os.Stdin)
	}()
	go func() {
		_, _ = io.Copy(os.Stdout, cpty)
	}()

	if _, err := cpty.Wait(context.Background()); err != nil {
		log.Printf("Wait error: %v", err)
	}
}
```
