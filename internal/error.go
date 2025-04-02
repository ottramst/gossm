package internal

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/gjbae1212/go-wraperror"
)

// Common error types for the application
var (
	// ErrInvalidParams is returned when function arguments are invalid
	ErrInvalidParams = errors.New("invalid parameters")

	// ErrUnknown is returned when the error reason cannot be determined
	ErrUnknown = errors.New("unknown error")
)

// WrapError wraps an error with file and line information for better debugging
// If the input error is nil, nil is returned
func WrapError(err error) error {
	if err == nil {
		return nil
	}

	// Get caller information
	pc, _, line, _ := runtime.Caller(1)

	// Extract function name
	fullFuncName := runtime.FuncForPC(pc).Name()
	funcNameParts := strings.Split(fullFuncName, "/")
	funcName := funcNameParts[len(funcNameParts)-1]

	// Create wrapped error with function name and line number
	chainErr := wraperror.Error(err)
	return chainErr.Wrap(fmt.Errorf("%s:%d", funcName, line))
}
