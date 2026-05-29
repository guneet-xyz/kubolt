package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/guneet-xyz/kubolt/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const (
	repoOwner = "guneet-xyz"
	repoName  = "kubolt"
)

var (
	apiLatestURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
	downloadBase = "https://github.com/" + repoOwner + "/" + repoName + "/releases/download"
)

var (
	upgradeRequireSignature bool
	upgradeSkipChecksum     bool

	cosignCertIdentityRegex = `https://github\.com/guneet-xyz/kubolt/\.github/workflows/release\.yml@.*`
	cosignOIDCIssuer        = `https://token.actions.githubusercontent.com`

	// Test seams.
	httpClient    = &http.Client{Timeout: 60 * time.Second}
	lookPath      = exec.LookPath
	replaceBinary = atomicReplace
)

var upgradeCmd = &cobra.Command{
	Use:          "upgrade",
	Short:        "Upgrade kubolt to the latest version",
	Long:         `Downloads the latest prebuilt release binary from GitHub and replaces the current binary.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runUpgrade(); err != nil {
			fmt.Fprintf(Stderr, "\nUpgrade failed. You can reinstall manually:\n  curl -sSL https://raw.githubusercontent.com/guneet-xyz/kubolt/main/install.sh | sh\n")
			return err
		}
		return nil
	},
}

func runUpgrade() error {
	return runUpgradeFromURLs(version.Version, apiLatestURL, downloadBase, runtime.GOOS, runtime.GOARCH)
}

func runUpgradeFromURLs(currentVersion, latestTagAPIURL, downloadBaseURL, goos, goarch string) error {
	current := currentVersion
	fmt.Fprintf(Stderr, "Current version: %s\n", current)

	fmt.Fprintln(Stderr, "Fetching latest version...")
	latest, err := fetchLatestTag(latestTagAPIURL)
	if err != nil {
		return fmt.Errorf("fetching latest version: %w", err)
	}
	if latest == "" {
		return fmt.Errorf("no release tags found")
	}

	fmt.Fprintf(Stderr, "Latest version: %s\n", latest)

	if current != "dev" && semver.Compare(current, latest) >= 0 {
		fmt.Fprintf(Stderr, "Already up to date (%s)\n", current)
		return nil
	}

	assetName, err := assetNameFor(latest, goos, goarch)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "kubolt-upgrade-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	assetPath := filepath.Join(tmpDir, assetName)
	assetURL := fmt.Sprintf("%s/%s/%s", downloadBaseURL, latest, assetName)

	fmt.Fprintf(Stderr, "Downloading %s...\n", assetName)
	if err := downloadFile(assetURL, assetPath); err != nil {
		return fmt.Errorf("downloading binary: %w", err)
	}

	sumsURL := fmt.Sprintf("%s/%s/checksums.txt", downloadBaseURL, latest)
	sumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadFile(sumsURL, sumsPath); err != nil {
		if upgradeSkipChecksum || os.Getenv("KUBOLT_INSECURE_SKIP_CHECKSUM") == "1" {
			fmt.Fprintln(Stderr, "warning: skipping checksum verification (KUBOLT_INSECURE_SKIP_CHECKSUM=1 / --insecure-skip-checksum)")
		} else {
			return fmt.Errorf("downloading checksums: %w (set --insecure-skip-checksum or KUBOLT_INSECURE_SKIP_CHECKSUM=1 to bypass; this is unsafe)", err)
		}
	} else {
		if err := verifyChecksum(assetPath, sumsPath, assetName); err != nil {
			return fmt.Errorf("verifying checksum: %w", err)
		}
		fmt.Fprintln(Stderr, "Checksum OK")
	}

	requireSig := upgradeRequireSignature || os.Getenv("KUBOLT_REQUIRE_COSIGN") == "1"
	sigURL := fmt.Sprintf("%s/%s/%s.sig", downloadBaseURL, latest, assetName)
	certURL := fmt.Sprintf("%s/%s/%s.pem", downloadBaseURL, latest, assetName)
	sigPath := filepath.Join(tmpDir, assetName+".sig")
	certPath := filepath.Join(tmpDir, assetName+".pem")

	if requireSig {
		if _, err := lookPath("cosign"); err != nil {
			return fmt.Errorf("cosign binary required for signature verification but not in PATH; install from https://github.com/sigstore/cosign/releases or unset KUBOLT_REQUIRE_COSIGN: %w", err)
		}
	}

	sigErr := downloadFile(sigURL, sigPath)
	certErr := downloadFile(certURL, certPath)
	if sigErr != nil || certErr != nil {
		if requireSig {
			return fmt.Errorf("cosign signature artifacts unavailable for %s (set --require-signature=false or unset KUBOLT_REQUIRE_COSIGN to bypass)", assetName)
		}
		fmt.Fprintln(Stderr, "note: no cosign signature available for this release (acceptable for legacy releases; opt into enforcement with --require-signature)")
	} else {
		if err := verifyCosignSignature(assetPath, sigPath, certPath, requireSig, Stderr); err != nil {
			return err
		}
	}

	if err := os.Chmod(assetPath, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	if err := replaceBinary(assetPath, execPath); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Fprintf(Stderr, "Upgraded kubolt from %s to %s\n", current, latest)
	return nil
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeSkipChecksum, "insecure-skip-checksum", false, "skip SHA-256 verification of the downloaded binary (UNSAFE)")
	upgradeCmd.Flags().BoolVar(&upgradeRequireSignature, "require-signature", false, "require cosign signature verification (default: warn-only)")
	rootCmd.AddCommand(upgradeCmd)
}

func verifyCosignSignature(binaryPath, sigPath, certPath string, requireSig bool, stderr io.Writer) error {
	path, err := lookPath("cosign")
	if err != nil {
		if requireSig {
			return fmt.Errorf("cosign binary required for signature verification but not in PATH; install from https://github.com/sigstore/cosign/releases or unset KUBOLT_REQUIRE_COSIGN: %w", err)
		}
		fmt.Fprintln(stderr, "note: cosign not in PATH; skipping signature verification (install cosign for stronger supply-chain guarantees)")
		return nil
	}
	cmd := exec.Command(path, "verify-blob",
		"--certificate-identity-regexp", cosignCertIdentityRegex,
		"--certificate-oidc-issuer", cosignOIDCIssuer,
		"--signature", sigPath,
		"--certificate", certPath,
		binaryPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if requireSig {
			return fmt.Errorf("cosign verification failed: %s: %w", string(out), err)
		}
		fmt.Fprintf(stderr, "warning: cosign verification failed (non-fatal in opportunistic mode): %s\n", string(out))
		return nil
	}
	fmt.Fprintln(stderr, "cosign signature verified")
	return nil
}

func assetNameFor(tag, goos, goarch string) (string, error) {
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", fmt.Errorf("unsupported OS: %s", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("kubolt-%s-%s-%s%s", tag, goos, goarch, ext), nil
}

func downloadFile(url, dst string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func verifyChecksum(filePath, sumsPath, name string) error {
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	var expected string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("no checksum entry for %s", name)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch: expected=%s actual=%s", expected, actual)
	}
	return nil
}

func fetchLatestTag(url string) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if !semver.IsValid(rel.TagName) {
		return "", fmt.Errorf("invalid tag from GitHub: %q", rel.TagName)
	}
	return rel.TagName, nil
}

func atomicReplace(src, dst string) error {
	srcData, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		oldPath := dst + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(dst, oldPath); err != nil {
			return fmt.Errorf("renaming existing binary: %w", err)
		}
		if err := os.WriteFile(dst, srcData, 0o755); err != nil {
			_ = os.Rename(oldPath, dst)
			return err
		}
		return nil
	}

	tmpFile := dst + ".tmp"
	if err := os.WriteFile(tmpFile, srcData, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, dst); err != nil {
		os.Remove(tmpFile)
		return err
	}
	return nil
}
