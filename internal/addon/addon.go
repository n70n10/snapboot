package addon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/n70n10/snapboot/internal/boot"
	"github.com/n70n10/snapboot/pkg/logger"
)

// BuildCmdline constructs the kernel cmdline override for a snapshot.
//
// It reads the current booted cmdline from /proc/cmdline, strips any existing
// rootflags= and snapboot.snapshot= parameters, then appends the correct
// rootflags for the given snapshot subvolume.
func BuildCmdline(snapshotID int) (string, error) {
	raw, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", fmt.Errorf("cannot read /proc/cmdline: %w", err)
	}

	params := strings.Fields(strings.TrimSpace(string(raw)))
	filtered := make([]string, 0, len(params))

	for _, p := range params {
		key := strings.SplitN(p, "=", 2)[0]
		if key == "rootflags" || key == "snapboot.snapshot" {
			continue
		}
		filtered = append(filtered, p)
	}

	subvol := ".snapshots/" + strconv.Itoa(snapshotID) + "/snapshot"
	filtered = append(filtered, "rootflags=subvol="+subvol)
	// Informational marker readable at runtime via /proc/cmdline.
	filtered = append(filtered, "snapboot.snapshot="+strconv.Itoa(snapshotID))

	return strings.Join(filtered, " "), nil
}

// Generate creates a UKI addon .efi at destPath containing only a .cmdline
// section, using objcopy. systemd-boot v253+ picks up addons from
// ESP/EFI/Linux/addons/ and merges their sections into the booted UKI.
//
// If a systemd stub PE is found it is used as the base (correct PE headers).
// Otherwise objcopy builds a minimal PE from the raw cmdline bytes — this
// fallback is sufficient for the .cmdline section but produces a non-runnable
// stub, which is fine since addons are never executed directly.
func Generate(cmdline string, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("cannot create addon dir: %w", err)
	}

	// Write cmdline bytes to a temp file. No trailing newline.
	tmp, err := os.CreateTemp("", "snapboot-cmdline-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(cmdline); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write temp cmdline: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	stub := findStub()

	var args []string
	if stub != "" {
		// Use the systemd stub as a proper PE base. objcopy appends
		// the .cmdline section, resulting in a valid addon .efi.
		args = []string{
			"--add-section", ".cmdline=" + tmpName,
			"--change-section-vma", ".cmdline=0x30000",
			stub,
			destPath,
		}
	} else {
		// Fallback: build from /dev/null with an explicit binary target.
		args = []string{
			"-I", "binary",
			"-O", "pei-x86-64",
			"--add-section", ".cmdline=" + tmpName,
			"--set-section-flags", ".cmdline=alloc,load,readonly,data",
			"--change-section-vma", ".cmdline=0x1000",
			"/dev/null",
			destPath,
		}
	}

	cmd := exec.Command("objcopy", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("objcopy failed (stub=%q): %w", stub, err)
	}

	logger.Info("generated addon: %s", filepath.Base(destPath))
	return nil
}

// findStub looks for a systemd PE stub to use as the addon base.
// Prefers the dedicated addon stub shipped since systemd v255.
func findStub() string {
	candidates := []string{
		"/usr/lib/systemd/boot/efi/addonx64.efi.stub", // systemd ≥ v255 dedicated addon stub
		"/usr/lib/systemd/boot/efi/linuxx64.efi.stub", // generic linux stub, works as fallback
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// SyncForSnapshot ensures an addon .efi exists and is up to date for the
// given snapshot ID.
//
// It is idempotent: regeneration is skipped when both the cmdline hash AND
// the base UKI hash match the recorded DB values, and the addon file is
// present on disk. This means a kernel upgrade (new base UKI) correctly
// forces a re-record of the KernelHash, even though the addon file content
// itself does not change (the cmdline for a given snapshot ID is stable).
//
// Returns: cmdline string, cmdline SHA256 hex hash, whether a new file was
// generated, and any error.
func SyncForSnapshot(esp string, snapshotID int, existingCmdlineHash string, existingKernelHash string, currentKernelHash string) (string, string, bool, error) {
	cmdline, err := BuildCmdline(snapshotID)
	if err != nil {
		return "", "", false, err
	}

	hash := boot.SHA256String(cmdline)
	addonPath := boot.AddonPath(esp, snapshotID)

	// Skip regenerating the file if:
	//   - cmdline is unchanged (same snapshot subvol path, same base params)
	//   - the addon file already exists on disk
	// We still return generated=false so the caller knows to update KernelHash
	// in the DB if currentKernelHash != existingKernelHash.
	if hash == existingCmdlineHash {
		if _, err := os.Stat(addonPath); err == nil {
			// File is present and cmdline unchanged — no need to regenerate.
			// Signal whether the kernel hash changed so caller can update DB.
			kernelChanged := currentKernelHash != existingKernelHash
			return cmdline, hash, kernelChanged, nil
		}
	}

	if err := Generate(cmdline, addonPath); err != nil {
		return "", "", false, err
	}
	return cmdline, hash, true, nil
}
