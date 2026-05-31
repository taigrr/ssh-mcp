package main

import "errors"

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
