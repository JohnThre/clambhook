//go:build !darwin && !linux

// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package procattr

// lookup is unsupported on platforms without a process/socket attribution
// backend; attribution is silently disabled.
func lookup(network, source string) (Process, bool) {
	return Process{}, false
}
