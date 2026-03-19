package btrfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultRootSubvol is the btrfs subvolume name used as the active root.
	DefaultRootSubvol = "@"
)

// Rollback performs a btrfs-based in-place swap of the root subvolume.
//
// Steps:
//  1. Detect the btrfs root block device from /proc/mounts.
//  2. Mount the btrfs top-level (subvolid=5) to a temp directory.
//  3. Rename current @ → @snapboot-backup-<timestamp>.
//  4. btrfs subvolume snapshot .snapshots/N/snapshot → @.
//  5. Unmount the temp mount.
//
// The caller is responsible for prompting the user before calling this.
// Reboot is required for the new @ to take effect.
func Rollback(snapshotID int) error {
	dev, err := findBtrfsDevice()
	if err != nil {
		return fmt.Errorf("cannot find btrfs root device: %w", err)
	}

	mnt, err := os.MkdirTemp("", "snapboot-mnt-*")
	if err != nil {
		return err
	}
	defer func() {
		exec.Command("umount", mnt).Run() //nolint:errcheck
		os.RemoveAll(mnt)
	}()

	mountCmd := exec.Command("mount", "-t", "btrfs", "-o", "subvolid=5", dev, mnt)
	mountCmd.Stdout = os.Stdout
	mountCmd.Stderr = os.Stderr
	if err := mountCmd.Run(); err != nil {
		return fmt.Errorf("cannot mount btrfs root (subvolid=5) from %s: %w", dev, err)
	}

	backupName := fmt.Sprintf("@snapboot-backup-%s", time.Now().Format("20060102T150405"))
	currentAt := filepath.Join(mnt, DefaultRootSubvol)
	backupAt := filepath.Join(mnt, backupName)

	// Rename current @ to backup.
	if err := os.Rename(currentAt, backupAt); err != nil {
		return fmt.Errorf("cannot rename @ → %s: %w", backupName, err)
	}
	fmt.Printf("  → renamed @ → %s\n", backupName)

	// Create new @ as a read-write snapshot of the target snapshot.
	src := filepath.Join(mnt, ".snapshots", strconv.Itoa(snapshotID), "snapshot")
	dst := filepath.Join(mnt, DefaultRootSubvol)

	snapCmd := exec.Command("btrfs", "subvolume", "snapshot", src, dst)
	snapCmd.Stdout = os.Stdout
	snapCmd.Stderr = os.Stderr
	if err := snapCmd.Run(); err != nil {
		// Attempt to restore the original @ from backup.
		if restoreErr := os.Rename(backupAt, currentAt); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "CRITICAL: could not restore @ from backup %s: %v\n", backupName, restoreErr)
			fmt.Fprintf(os.Stderr, "Manually run: mv %s %s\n", backupAt, currentAt)
		} else {
			fmt.Fprintf(os.Stderr, "  restored @ from backup after failure\n")
		}
		return fmt.Errorf("btrfs snapshot failed: %w", err)
	}

	fmt.Printf("  → created new @ from .snapshots/%d/snapshot\n", snapshotID)
	return nil
}

// findBtrfsDevice returns the block device for the btrfs root filesystem
// by parsing /proc/mounts. Looks for mountpoint "/" with fstype "btrfs".
func findBtrfsDevice() (string, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		device, mountpoint, fstype := fields[0], fields[1], fields[2]
		if mountpoint == "/" && fstype == "btrfs" {
			return device, nil
		}
	}
	return "", fmt.Errorf("no btrfs root mount found in /proc/mounts")
}
