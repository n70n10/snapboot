package hooks

import (
	"fmt"
	"os"
	"path/filepath"
)

// Snapper hook paths — snapper calls executables in these directories
// after snapshot operations. The SNAPPER_CONFIG and SNAPPER_SNAPSHOT_NUM
// environment variables are set by snapper.
const (
	snapperPostCreateDir = "/etc/snapper/hooks/post-create.d"
	snapperPostDeleteDir = "/etc/snapper/hooks/post-delete.d"
	snapperHookName      = "snapboot"
)

// Pacman hook path — alpm hooks in /etc/pacman.d/hooks/ run after
// package transactions matching the trigger.
const (
	pacmanHookDir  = "/etc/pacman.d/hooks"
	pacmanHookName = "99-snapboot.hook"
)

// snapperPostCreateScript is called by snapper after a new snapshot is created.
// SNAPPER_CONFIG and SNAPPER_SNAPSHOT_NUM are set in the environment.
const snapperPostCreateScript = `#!/bin/bash
# Managed by snapboot — do not edit manually.
set -euo pipefail

# Only act on the root config.
[[ "${SNAPPER_CONFIG:-}" == "root" ]] || exit 0

exec /usr/local/bin/snapboot sync --snapshot "${SNAPPER_SNAPSHOT_NUM}"
`

// snapperPostDeleteScript is called by snapper after a snapshot is deleted.
const snapperPostDeleteScript = `#!/bin/bash
# Managed by snapboot — do not edit manually.
set -euo pipefail

[[ "${SNAPPER_CONFIG:-}" == "root" ]] || exit 0

exec /usr/local/bin/snapboot clean
`

// pacmanHookContent is an alpm hook that triggers a full snapboot sync after
// any transaction that installs or upgrades the kernel or dracut. This ensures
// the KernelHash recorded in the DB stays current, and catches cases where a
// kernel upgrade produces a new base UKI with a different name.
//
// The hook runs *after* the transaction (PostTransaction) so dracut has already
// rebuilt the UKI by the time snapboot runs.
const pacmanHookContent = `# Managed by snapboot — do not edit manually.
[Trigger]
Operation = Install
Operation = Upgrade
Type = Package
Target = linux
Target = linux-lts
Target = linux-zen
Target = linux-hardened
Target = dracut

[Action]
Description = Updating snapboot boot entries after kernel/dracut upgrade...
When = PostTransaction
Exec = /usr/local/bin/snapboot sync
Depends = snapboot
`

// InstallSnapper writes the snapper post-create and post-delete hook scripts.
func InstallSnapper(binaryPath string) error {
	_ = binaryPath // path is hardcoded in the scripts for clarity

	for _, dir := range []string{snapperPostCreateDir, snapperPostDeleteDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create %s: %w", dir, err)
		}
	}

	scripts := []struct {
		path    string
		content string
	}{
		{filepath.Join(snapperPostCreateDir, snapperHookName), snapperPostCreateScript},
		{filepath.Join(snapperPostDeleteDir, snapperHookName), snapperPostDeleteScript},
	}
	for _, s := range scripts {
		if err := writeExecutable(s.path, s.content); err != nil {
			return err
		}
		fmt.Printf("  installed snapper hook: %s\n", s.path)
	}
	return nil
}

// UninstallSnapper removes the snapper hook scripts.
func UninstallSnapper() error {
	paths := []string{
		filepath.Join(snapperPostCreateDir, snapperHookName),
		filepath.Join(snapperPostDeleteDir, snapperHookName),
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot remove %s: %w", p, err)
		}
		fmt.Printf("  removed: %s\n", p)
	}
	return nil
}

// InstallPacman writes the pacman alpm hook file.
func InstallPacman(binaryPath string) error {
	_ = binaryPath

	if err := os.MkdirAll(pacmanHookDir, 0755); err != nil {
		return fmt.Errorf("cannot create %s: %w", pacmanHookDir, err)
	}
	hookPath := filepath.Join(pacmanHookDir, pacmanHookName)
	if err := os.WriteFile(hookPath, []byte(pacmanHookContent), 0644); err != nil {
		return fmt.Errorf("cannot write %s: %w", hookPath, err)
	}
	fmt.Printf("  installed pacman hook:  %s\n", hookPath)
	return nil
}

// UninstallPacman removes the pacman alpm hook.
func UninstallPacman() error {
	hookPath := filepath.Join(pacmanHookDir, pacmanHookName)
	if err := os.Remove(hookPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove %s: %w", hookPath, err)
	}
	fmt.Printf("  removed: %s\n", hookPath)
	return nil
}

func writeExecutable(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}
