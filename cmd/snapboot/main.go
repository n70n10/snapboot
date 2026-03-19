package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/n70n10/snapboot/internal/addon"
	"github.com/n70n10/snapboot/internal/boot"
	"github.com/n70n10/snapboot/internal/btrfs"
	"github.com/n70n10/snapboot/internal/config"
	"github.com/n70n10/snapboot/internal/db"
	"github.com/n70n10/snapboot/internal/hooks"
	"github.com/n70n10/snapboot/internal/snapper"
	"github.com/n70n10/snapboot/pkg/logger"
)

const version = "0.1.0"

func usage() {
	fmt.Fprintf(os.Stderr, `snapboot %s — btrfs snapshot boot manager for systemd-boot + dracut UKIs

Usage:
  snapboot <command> [flags]

Commands:
  sync [--snapshot N]   Create/update addon .efi for all snapshots (or just N)
  list                  List snapshots and their boot addon status
  rollback <N>          Promote snapshot N to be the new root (btrfs in-place swap)
  clean                 Remove addon .efi files for snapshots that no longer exist
  install               Install snapper + pacman hooks, create required directories
  uninstall             Remove all hooks and generated addon .efi files
  version               Print version and exit

Flags:
  --snapshot N          (sync only) Process only snapshot ID N
  --config PATH         Path to config file (default: /etc/snapboot/snapboot.conf)

Notes:
  Addon .efi files live at ESP/EFI/Linux/addons/snapboot-N.efi
  systemd-boot picks these up automatically; the base UKI is never modified.
  Config file: /etc/snapboot/snapboot.conf

`, version)
	os.Exit(1)
}

func main() {
	if os.Getuid() != 0 {
		logger.Fatal("snapboot must run as root")
	}

	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

	switch args[0] {
	case "sync":
		cmdSync(args[1:])
	case "list":
		cmdList(args[1:])
	case "rollback":
		cmdRollback(args[1:])
	case "clean":
		cmdClean(args[1:])
	case "install":
		cmdInstall(args[1:])
	case "uninstall":
		cmdUninstall(args[1:])
	case "version":
		fmt.Println("snapboot", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		usage()
	}
}

// loadConfig parses --config from args and loads the config file.
// Returns the config and the remaining args with --config consumed.
func loadConfig(args []string) (*config.Config, []string) {
	cfgPath := config.DefaultPath
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
		} else {
			remaining = append(remaining, args[i])
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Fatal("cannot load config: %v", err)
	}
	return cfg, remaining
}

// cmdSync reconciles snapper snapshots with addon .efi files on the ESP.
// With --snapshot N, only processes that one snapshot (called from the
// snapper post-create hook). Without it, reconciles all snapshots (called
// after kernel upgrades via the pacman hook).
func cmdSync(args []string) {
	cfg, args := loadConfig(args)

	targetID := -1
	for i, a := range args {
		if a == "--snapshot" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				logger.Fatal("invalid snapshot ID: %s", args[i+1])
			}
			targetID = n
		}
	}

	database, err := db.Load(cfg.DBPath)
	if err != nil {
		logger.Fatal("cannot load database: %v", err)
	}

	if err := boot.EnsureAddonDir(cfg.ESP); err != nil {
		logger.Fatal("cannot create addon dir: %v", err)
	}

	baseUKI, err := boot.LatestBaseUKI(cfg.ESP)
	if err != nil {
		logger.Fatal("cannot find base UKI: %v", err)
	}
	logger.Info("base UKI: %s  (sha256: %s...)", baseUKI.Name, baseUKI.SHA256[:16])

	var snapshots []snapper.Snapshot

	if targetID >= 0 {
		// Single-snapshot mode (invoked from snapper post-create hook).
		if !snapper.Exists(targetID) {
			logger.Fatal("snapshot %d does not exist on disk", targetID)
		}
		desc := ""
		if all, err := snapper.List(cfg.SnapperConfig); err == nil {
			for _, s := range all {
				if s.ID == targetID {
					desc = s.Description
					break
				}
			}
		}
		snapshots = []snapper.Snapshot{{
			ID:          targetID,
			SubvolPath:  snapper.GetSubvolPath(targetID),
			Description: desc,
		}}
	} else {
		// Full sync mode (initial setup or post-kernel-upgrade).
		snapshots, err = snapper.List(cfg.SnapperConfig)
		if err != nil {
			logger.Fatal("cannot list snapshots: %v", err)
		}
		logger.Info("found %d snapshot(s) to reconcile", len(snapshots))
	}

	created := 0
	updated := 0 // kernel hash updated but addon file unchanged
	skipped := 0
	failed := 0

	for _, s := range snapshots {
		existing, _ := database.Get(s.ID)
		existingCmdlineHash := ""
		existingKernelHash := ""
		if existing != nil {
			existingCmdlineHash = existing.CmdlineHash
			existingKernelHash = existing.KernelHash
		}

		_, hash, needsUpdate, err := addon.SyncForSnapshot(
			cfg.ESP, s.ID,
			existingCmdlineHash, existingKernelHash, baseUKI.SHA256,
		)
		if err != nil {
			logger.Error("snapshot %d: %v", s.ID, err)
			failed++
			continue
		}

		isNew := existing == nil
		cmdlineChanged := hash != existingCmdlineHash
		kernelChanged := baseUKI.SHA256 != existingKernelHash

		switch {
		case isNew || cmdlineChanged:
			// New snapshot or cmdline changed: write a fresh DB record.
			// (needsUpdate will be true here — the addon file was just generated.)
			entry := &db.AddonEntry{
				SnapshotID:  s.ID,
				SubvolPath:  s.SubvolPath,
				KernelHash:  baseUKI.SHA256,
				BaseUKI:     baseUKI.Path,
				AddonPath:   boot.AddonPath(cfg.ESP, s.ID),
				CmdlineHash: hash,
				Description: s.Description,
				CreatedAt:   time.Now().Format(time.RFC3339),
			}
			database.Set(s.ID, entry)
			created++

		case kernelChanged:
			// Kernel upgraded but cmdline is stable: the addon file content is
			// unchanged (needsUpdate signals this), just update the DB metadata.
			existing.KernelHash = baseUKI.SHA256
			existing.BaseUKI = baseUKI.Path
			database.Set(s.ID, existing)
			logger.Info("snapshot %d: recorded new kernel hash", s.ID)
			updated++

		default:
			// needsUpdate is false — everything is current.
			_ = needsUpdate
			skipped++
		}
	}

	if err := database.Save(); err != nil {
		logger.Error("cannot save database: %v", err)
	}

	logger.Info("sync complete — created: %d  kernel-updated: %d  up-to-date: %d  failed: %d",
		created, updated, skipped, failed)
}

// cmdList prints all tracked and known snapshots with their addon status.
func cmdList(args []string) {
	cfg, _ := loadConfig(args)

	database, err := db.Load(cfg.DBPath)
	if err != nil {
		logger.Fatal("cannot load database: %v", err)
	}

	snapshots, snapErr := snapper.List(cfg.SnapperConfig)
	if snapErr != nil {
		logger.Warn("cannot query snapper (%v) — showing DB records only", snapErr)
	}
	snapMap := make(map[int]snapper.Snapshot)
	for _, s := range snapshots {
		snapMap[s.ID] = s
	}

	// Union of IDs from snapper and DB.
	idSet := make(map[int]bool)
	for id := range snapMap {
		idSet[id] = true
	}
	for _, e := range database.All() {
		idSet[e.SnapshotID] = true
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	if len(ids) == 0 {
		fmt.Println("No snapshots found.")
		return
	}

	// Find the current base UKI hash for staleness indicator (best-effort).
	currentKernelHash := ""
	if baseUKI, err := boot.LatestBaseUKI(cfg.ESP); err == nil {
		currentKernelHash = baseUKI.SHA256
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Println()
	logger.Header("Snapshots")
	fmt.Println()
	fmt.Fprintln(w, "  ID\tTYPE\tDATE\tDESCRIPTION\tSTATUS\tADDON FILE")
	fmt.Fprintln(w, "  --\t----\t----\t-----------\t------\t----------")

	for _, id := range ids {
		s := snapMap[id]
		entry, hasEntry := database.Get(id)

		snapExists := snapper.Exists(id)
		status := "not-synced"
		addonFile := "-"

		if hasEntry {
			addonFile = fmt.Sprintf("snapboot-%d.efi", id)
			if _, err := os.Stat(entry.AddonPath); err == nil {
				if !snapExists {
					status = "orphaned"
				} else if currentKernelHash != "" && entry.KernelHash != currentKernelHash {
					status = "stale-kernel" // kernel upgraded, run sync
				} else {
					status = "ok"
				}
			} else {
				status = "file-missing"
			}
		}

		typeStr := s.Type
		if typeStr == "" {
			typeStr = "-"
		}
		dateStr := s.Date
		if dateStr == "" && hasEntry {
			dateStr = entry.CreatedAt
		}
		desc := s.Description
		if desc == "" && hasEntry {
			desc = entry.Description
		}
		if desc == "" {
			desc = "-"
		}
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}

		fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%s\t%s\n",
			id, typeStr, dateStr, desc, status, addonFile)
	}
	w.Flush()

	fmt.Println()
	fmt.Printf("  ESP       : %s\n", cfg.ESP)
	fmt.Printf("  Addon dir : %s\n", boot.AddonDir(cfg.ESP))
	fmt.Printf("  DB        : %s\n", cfg.DBPath)
	fmt.Println()
	fmt.Println("  Statuses: ok | not-synced | stale-kernel | file-missing | orphaned")
	fmt.Println("  Run 'snapboot sync' to generate or update missing/stale addons.")
	fmt.Println()
}

// cmdRollback performs a btrfs in-place swap of @ with snapshot N.
func cmdRollback(args []string) {
	cfg, args := loadConfig(args)
	_ = cfg // cfg used for future subvol config; rollback uses /proc/mounts

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: snapboot rollback <snapshot-id>")
		os.Exit(1)
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		logger.Fatal("invalid snapshot ID: %s", args[0])
	}

	if !snapper.Exists(id) {
		logger.Fatal("snapshot %d not found at %s", id, snapper.GetSubvolPath(id))
	}

	fmt.Println()
	logger.Header("Rollback to snapshot %d", id)
	fmt.Println()
	fmt.Println("  This operation will:")
	fmt.Printf("    1. Rename the current @ subvolume → @snapboot-backup-<timestamp>\n")
	fmt.Printf("    2. Create a new read-write @ from .snapshots/%d/snapshot\n", id)
	fmt.Println("    3. Leave all snapper snapshots and addon .efi files untouched")
	fmt.Println()
	fmt.Println("  Your previous @ is preserved on the btrfs pool as a backup subvolume.")
	fmt.Println("  You MUST REBOOT for the change to take effect.")
	fmt.Println()
	fmt.Print("  Proceed? [y/N] ")

	var confirm string
	fmt.Scanln(&confirm)
	if !strings.EqualFold(strings.TrimSpace(confirm), "y") {
		fmt.Println("  Aborted.")
		return
	}

	fmt.Println()
	if err := btrfs.Rollback(id); err != nil {
		logger.Fatal("rollback failed: %v", err)
	}

	fmt.Println()
	logger.Info("rollback complete")
	fmt.Println()
	fmt.Printf("  Snapshot %d has been promoted to @.\n", id)
	fmt.Println("  Please reboot now for the change to take effect.")
	fmt.Println("  After rebooting, your previous root is preserved as")
	fmt.Println("  @snapboot-backup-<timestamp> on the btrfs pool (subvolid=5).")
	fmt.Println("  Delete it manually when no longer needed:")
	fmt.Println("    mount -o subvolid=5 /dev/sdX /mnt")
	fmt.Println("    btrfs subvolume delete /mnt/@snapboot-backup-<timestamp>")
	fmt.Println()
}

// cmdClean removes addon .efi files for snapshots that no longer exist on disk.
func cmdClean(args []string) {
	cfg, _ := loadConfig(args)

	database, err := db.Load(cfg.DBPath)
	if err != nil {
		logger.Fatal("cannot load database: %v", err)
	}

	removed := 0
	for _, entry := range database.All() {
		if snapper.Exists(entry.SnapshotID) {
			continue
		}
		if err := os.Remove(entry.AddonPath); err != nil && !os.IsNotExist(err) {
			logger.Error("cannot remove %s: %v", entry.AddonPath, err)
			continue
		}
		database.Delete(entry.SnapshotID)
		logger.Info("removed orphaned addon: snapshot %d → %s",
			entry.SnapshotID, entry.AddonPath)
		removed++
	}

	if err := database.Save(); err != nil {
		logger.Error("cannot save database: %v", err)
	}

	if removed == 0 {
		logger.Info("nothing to clean — all addons have live snapshots")
	} else {
		logger.Info("clean complete — removed %d orphaned addon(s)", removed)
	}
}

// cmdInstall sets up snapper hooks, pacman hook, config file, and directories.
func cmdInstall(args []string) {
	cfg, _ := loadConfig(args)

	fmt.Println()
	logger.Header("Installing snapboot")
	fmt.Println()

	// State directory
	if err := os.MkdirAll("/var/lib/snapboot", 0755); err != nil {
		logger.Fatal("cannot create state dir: %v", err)
	}
	fmt.Println("  created: /var/lib/snapboot/")

	// Addon directory on ESP
	if err := boot.EnsureAddonDir(cfg.ESP); err != nil {
		logger.Fatal("cannot create addon dir on ESP: %v", err)
	}
	fmt.Printf("  created: %s\n", boot.AddonDir(cfg.ESP))

	// Config file (only if it doesn't already exist)
	if err := os.MkdirAll("/etc/snapboot", 0755); err != nil {
		logger.Fatal("cannot create config dir: %v", err)
	}
	if _, err := os.Stat(config.DefaultPath); os.IsNotExist(err) {
		if err := os.WriteFile(config.DefaultPath, []byte(config.DefaultFileContent()), 0644); err != nil {
			logger.Fatal("cannot write config file: %v", err)
		}
		fmt.Printf("  created: %s\n", config.DefaultPath)
	} else {
		fmt.Printf("  exists:  %s (not overwritten)\n", config.DefaultPath)
	}

	// Snapper hooks
	binaryPath, _ := os.Executable()
	if err := hooks.InstallSnapper(binaryPath); err != nil {
		logger.Fatal("cannot install snapper hooks: %v", err)
	}

	// Pacman hook
	if err := hooks.InstallPacman(binaryPath); err != nil {
		logger.Fatal("cannot install pacman hook: %v", err)
	}

	fmt.Println()
	logger.Info("install complete")
	fmt.Println()
	fmt.Println("  Next steps:")
	fmt.Println("    snapboot sync      — generate addons for all existing snapshots")
	fmt.Println("    snapboot list      — verify addon status")
	fmt.Println()
	fmt.Println("  Automation:")
	fmt.Println("    Snapper hooks handle snapshot create/delete automatically.")
	fmt.Println("    Pacman hook runs 'snapboot sync' after kernel/dracut upgrades.")
	fmt.Println()
}

// cmdUninstall removes hooks, config, state, and all generated addon .efi files.
func cmdUninstall(args []string) {
	cfg, _ := loadConfig(args)

	fmt.Println()
	logger.Header("Uninstalling snapboot")
	fmt.Println()

	// Remove all addon files tracked in DB
	database, _ := db.Load(cfg.DBPath)
	if database != nil {
		for _, entry := range database.All() {
			if err := os.Remove(entry.AddonPath); err != nil && !os.IsNotExist(err) {
				logger.Warn("cannot remove %s: %v", entry.AddonPath, err)
			} else {
				fmt.Printf("  removed addon: %s\n", entry.AddonPath)
			}
		}
	}

	// Remove state
	os.Remove(cfg.DBPath)
	os.Remove("/var/lib/snapboot")
	fmt.Println("  removed: /var/lib/snapboot/db.json")

	// Remove config
	os.Remove(config.DefaultPath)
	os.Remove("/etc/snapboot")
	fmt.Printf("  removed: %s\n", config.DefaultPath)

	// Remove hooks
	if err := hooks.UninstallSnapper(); err != nil {
		logger.Warn("snapper hook removal: %v", err)
	}
	if err := hooks.UninstallPacman(); err != nil {
		logger.Warn("pacman hook removal: %v", err)
	}

	fmt.Println()
	logger.Info("uninstall complete — base UKI files are untouched")
	fmt.Println()
}
