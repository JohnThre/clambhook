//go:build !darwin && !linux

// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package netwatch

func current() (NetworkInfo, error) {
	return NetworkInfo{}, nil
}
