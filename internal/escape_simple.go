package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatih/color"
	"golang.org/x/term"
)

// CallProcessWithSimpleEscape executes a process with simple escape sequence support
// This version passes stdin directly to avoid echo issues
func CallProcessWithSimpleEscape(process string, args ...string) error {
	// Check if stdin is a terminal
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Not a terminal, fall back to direct process execution
		return CallProcessDirect(process, args...)
	}

	// Create command with direct stdin/stdout/stderr
	cmd := exec.Command(process, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	
	// Create a pipe for stdin so we can monitor it
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return WrapError(err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return WrapError(err)
	}

	// Set terminal to raw mode to capture escape sequences
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// If we can't set raw mode, just pass through directly
		cmd.Stdin = os.Stdin
		return cmd.Wait()
	}
	
	// Ensure we restore terminal state on exit
	defer func() {
		term.Restore(int(os.Stdin.Fd()), oldState)
	}()
	
	// Add a small delay then print newline to fix prompt alignment
	// The SSM plugin prints "Starting session..." without a newline
	go func() {
		time.Sleep(100 * time.Millisecond)
		fmt.Fprintf(os.Stderr, "\r\n")
	}()

	// Set up contexts and channels
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle stdin copying with escape detection
	stdinErr := make(chan error, 1)
	escapeDetected := make(chan bool, 1)
	
	go func() {
		stdinErr <- copyWithEscapeDetection(ctx, stdinPipe, os.Stdin, escapeDetected)
	}()

	// Set up signal handling
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	// Wait for process completion, escape sequence, or signal
	processDone := make(chan error, 1)
	go func() {
		processDone <- cmd.Wait()
	}()

	select {
	case err := <-processDone:
		// Process exited normally
		cancel()
		// Add newline before the "Exiting session" message for proper alignment
		fmt.Fprintf(os.Stderr, "\r\n")
		return err
		
	case <-escapeDetected:
		// Escape sequence detected
		cancel()
		stdinPipe.Close()
		
		// Restore terminal before printing
		term.Restore(int(os.Stdin.Fd()), oldState)
		
		fmt.Fprintf(os.Stderr, "\r\n%s\r\n", 
			color.YellowString("Escape sequence detected. Terminating session..."))
		
		// Terminate the process gracefully
		return terminateGracefully(cmd)
		
	case sig := <-sigs:
		// Signal received
		cancel()
		stdinPipe.Close()
		cmd.Process.Signal(sig)
		<-processDone
		return nil
		
	case err := <-stdinErr:
		// Stdin copy error (likely process died)
		cancel()
		<-processDone
		return err
	}
}

// copyWithEscapeDetection copies stdin to the process while detecting escape sequences
func copyWithEscapeDetection(ctx context.Context, dst io.WriteCloser, src io.Reader, escapeDetected chan<- bool) error {
	defer dst.Close()
	
	lastWasNewline := true
	tildeSeen := false
	buf := make([]byte, 1)
	
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			n, err := src.Read(buf)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			
			if n == 0 {
				continue
			}
			
			b := buf[0]
			
			// Check for escape sequence only at start of line
			if lastWasNewline && b == '~' {
				tildeSeen = true
				lastWasNewline = false
				continue // Don't send the tilde yet
			} else if tildeSeen {
				if b == '.' {
					// Escape sequence complete
					escapeDetected <- true
					return nil
				} else {
					// Not an escape sequence, send the tilde and current char
					// This handles ~/, ~user, ~~, and any other ~ usage
					dst.Write([]byte{'~', b})
				}
				tildeSeen = false
				if b == '\r' || b == '\n' {
					lastWasNewline = true
				} else {
					lastWasNewline = false
				}
			} else {
				// Normal character
				dst.Write([]byte{b})
				if b == '\r' || b == '\n' {
					lastWasNewline = true
				} else {
					lastWasNewline = false
				}
			}
		}
	}
}

// terminateGracefully attempts to terminate a process gracefully
func terminateGracefully(cmd *exec.Cmd) error {
	// Send SIGTERM first
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may have already exited
		return nil
	}
	
	// Wait for graceful termination with timeout
	done := make(chan error, 1)
	go func() {
		_, err := cmd.Process.Wait()
		done <- err
	}()
	
	select {
	case err := <-done:
		// Process exited gracefully
		if err != nil && 
		   err.Error() != "signal: terminated" && 
		   err.Error() != "signal: broken pipe" &&
		   !isWaitError(err) {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s\r\n", 
			color.GreenString("Session terminated gracefully"))
		return nil
	case <-time.After(3 * time.Second):
		// Timeout reached, force kill
		fmt.Fprintf(os.Stderr, "%s\r\n", 
			color.YellowString("Graceful termination timed out, forcing exit..."))
		if err := cmd.Process.Kill(); err != nil {
			// Process may have already exited
			return nil
		}
		<-done // Wait for process to actually exit
		return nil
	}
}

// isWaitError checks if the error is a wait-related error that we can ignore
func isWaitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errStr == "wait: no child processes" || 
	       errStr == "waitid: no child processes"
}