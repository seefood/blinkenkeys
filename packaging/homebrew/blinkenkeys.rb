# DRAFT, not yet tested with `brew install`/`brew audit`. Lives in this repo for
# review; it belongs in the tap repo seefood/homebrew-blinkenkeys as
# Formula/blinkenkeys.rb. VERSION and the four sha256 values must be filled from
# a release's SHA256SUMS (a release job can automate this).
class Blinkenkeys < Formula
  desc "Daemon and CLI that drive per-key RGB on VialRGB keyboards"
  homepage "https://github.com/seefood/blinkenkeys"
  version "VERSION" # e.g. "0.1.0" (release tag is v#{version})
  license "GPL-3.0-only" # README says "GPL-3.0"; confirm -only vs -or-later

  base = "https://github.com/seefood/blinkenkeys/releases/download/v#{version}"

  on_macos do
    on_arm do
      url "#{base}/blinkenkeys-v#{version}-darwin-arm64.tar.gz"
      sha256 "SHA256_DARWIN_ARM64"
    end
    on_intel do
      url "#{base}/blinkenkeys-v#{version}-darwin-amd64.tar.gz"
      sha256 "SHA256_DARWIN_AMD64"
    end
  end

  on_linux do
    on_arm do
      url "#{base}/blinkenkeys-v#{version}-linux-arm64.tar.gz"
      sha256 "SHA256_LINUX_ARM64"
    end
    on_intel do
      url "#{base}/blinkenkeys-v#{version}-linux-amd64.tar.gz"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  def install
    bin.install "bin/blinkenkeysd", "bin/blincli"
    pkgshare.install "examples/config"
    pkgshare.install "integrations"
  end

  # Runs the daemon as a login service on macOS (and a systemd user unit on
  # Linuxbrew). It does not replace packaging/linux/install.sh's udev rule.
  service do
    run [opt_bin/"blinkenkeysd"]
    keep_alive true
    log_path var/"log/blinkenkeysd.log"
    error_log_path var/"log/blinkenkeysd.log"
  end

  def caveats
    <<~EOS
      Seed a config before starting the service, then edit the `devices:` uid:
        mkdir -p ~/.config/blinkenkeys
        cp -Rn #{pkgshare}/config/. ~/.config/blinkenkeys/

      Start at login:  brew services start blinkenkeys

      Linux: raw HID access needs a udev rule, which Homebrew cannot install.
      Use packaging/linux/install.sh from the release tarball for that.
    EOS
  end

  test do
    assert_match "blincli", shell_output("#{bin}/blincli version")
    system bin/"blinkenkeysd", "--check-config", "-c", pkgshare/"config"
  end
end
