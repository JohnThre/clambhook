# Internal developer QA formula only. Do not publish Homebrew releases for
# end-user distribution from GitHub.
class Clambhook < Formula
  desc "Local connectivity utility with terminal interface"
  homepage "https://github.com/JohnThre/clambhook"
  url "https://github.com/JohnThre/clambhook.git",
      tag:      "v0.1.0"
  license "GPL-3.0-only"

  depends_on "go" => :build
  depends_on "pkgconf" => :build
  depends_on "libsodium"

  def install
    system "make", "build", "VERSION=#{version}"
    bin.install "bin/clambhook"
    bin.install "bin/clambhook-tui"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/clambhook -version")
    assert_match version.to_s, shell_output("#{bin}/clambhook-tui -version")
  end
end
