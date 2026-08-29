# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Internal developer QA formula only. End-user macOS distribution is the
# signed and notarized SwiftUI application from GitHub Releases.
class Clambhook < Formula
  desc "C17 local connectivity daemon and terminal interface"
  homepage "https://github.com/JohnThre/clambhook"
  url "https://github.com/JohnThre/clambhook.git", tag: "v1.0.2"
  license "GPL-3.0-only"

  depends_on "cmake" => :build
  depends_on "ninja" => :build
  depends_on "pkgconf" => :build
  depends_on "curl"
  depends_on "libuv"
  depends_on "libsodium"
  depends_on "openssl@3"

  def install
    system "cmake", "-S", ".", "-B", "build", "-G", "Ninja",
           "-DCMAKE_BUILD_TYPE=Release",
           "-DCLAMBHOOK_BUILD_TESTS=OFF",
           "-DCLAMBHOOK_WARNINGS_AS_ERRORS=ON",
           *std_cmake_args
    system "cmake", "--build", "build"
    system "cmake", "--install", "build"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/clambhook --version")
    assert_match version.to_s, shell_output("#{bin}/clambhook-tui --version")
  end
end
