package main

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the SSH manager and transfer subsystem.
// Callers may use errors.Is to detect specific failure conditions.
var (
	// ErrSessionNotFound indicates the requested session ID does not exist.
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionInactive indicates the session exists but is no longer active.
	ErrSessionInactive = errors.New("session is not active")

	// ErrHostNotAllowed indicates the host alias is not in the allowed list.
	ErrHostNotAllowed = errors.New("host is not in the allowed hosts list")

	// ErrNoAuthMethods indicates no authentication methods could be assembled
	// for the requested host (no agent, no keys, no password).
	ErrNoAuthMethods = errors.New("no authentication methods available")
)

// TransferError describes a failure during a recursive SFTP transfer. It
// carries the number of files that were successfully transferred before the
// failure so callers can report progress. Use errors.AsType[*TransferError]
// to extract the structured information from a wrapped error.
type TransferError struct {
	// Op is the transfer direction ("upload" or "download").
	Op string
	// Transferred is the number of files successfully copied before failure.
	Transferred int
	// Path is the source path that triggered the failure when known.
	Path string
	// Err is the underlying cause.
	Err error
}

func (e *TransferError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s failed at %s after %d file(s): %v", e.Op, e.Path, e.Transferred, e.Err)
	}
	return fmt.Sprintf("%s failed after %d file(s): %v", e.Op, e.Transferred, e.Err)
}

func (e *TransferError) Unwrap() error { return e.Err }
