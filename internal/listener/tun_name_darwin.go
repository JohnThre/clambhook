//go:build darwin

// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package listener

func platformDefaultTUNName() string { return "utun" }
