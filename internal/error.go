package internal

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Common error types for the application
var (
	// ErrInvalidParams is returned when function arguments are invalid
	ErrInvalidParams = errors.New("invalid parameters")

	// ErrUnknown is returned when the error reason cannot be determined
	ErrUnknown = errors.New("unknown error")
)

// WrapError annotates an error with the calling function and line for better
// debugging. If the input error is nil, nil is returned. The result unwraps
// to the original error, so errors.Is/As keep working.
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

	return fmt.Errorf("%s:%d: %w", funcName, line, err)
}
