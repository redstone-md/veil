# Veil VPN Homebrew formula.
#
# Lives upstream at this path so the source-of-truth tracks the
# project. To consume it, the Homebrew tap repository
# (`redstone-md/homebrew-tap`) symlinks (or copies on release) this
# file into its own `Formula/` directory.
#
# Per-release operator workflow:
#
#   1. veil v$NEW is tagged + the release.yml run completes.
#   2. The release-bot updates `version`, `url`, and the `sha256`
#      values in this file from the published `checksums.txt`.
#   3. Commits to the tap repo. `brew install veil-vpn/tap/veil`
#      then picks up the new bottle.

class Veil < Formula
  desc       "Self-hosted, censorship-resistant VPN platform"
  homepage   "https://github.com/redstone-md/veil"
  license    "Apache-2.0"
  version    "0.0.0-placeholder"

  on_macos do
    on_arm do
      url "https://github.com/redstone-md/veil/releases/download/v#{version}/veil-darwin-arm64"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      url "https://github.com/redstone-md/veil/releases/download/v#{version}/veil-darwin-amd64"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/redstone-md/veil/releases/download/v#{version}/veil-linux-arm64"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      url "https://github.com/redstone-md/veil/releases/download/v#{version}/veil-linux-amd64"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    binary = "veil-#{OS.mac? ? "darwin" : "linux"}-#{Hardware::CPU.arch}"
    bin.install binary => "veil"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/veil version")
  end
end
