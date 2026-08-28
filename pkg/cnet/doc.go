//go:build unix

// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: Apache-2.0

// Package cnet provides Go bindings to clambhook's C performance layer.
//
// It wraps the cryptographic primitives, packet processing, and buffer
// management implemented in clib/ via cgo.
package cnet
