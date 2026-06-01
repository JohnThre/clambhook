//go:build unix

// Package cnet provides Go bindings to clambhook's C performance layer.
//
// It wraps the cryptographic primitives, packet processing, and buffer
// management implemented in clib/ via cgo.
package cnet
