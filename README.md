# snapboot

Btrfs snapshot boot manager for **systemd-boot + dracut UKI** setups on Arch Linux.

Automatically creates tiny UKI addon `.efi` files for each snapper snapshot so you
can select and boot into any snapshot directly from the systemd-boot menu — with
**zero duplication** of the large base UKI produced by dracut.

## How it works

systemd-boot v253+ supports **UKI addons**: tiny `.efi` files in
`ESP/EFI/Linux/addons/` that contain only a `.cmdline` section. When systemd-boot
boots a UKI it merges any matching addons, overriding the baked-in cmdline.
snapboot generates one addon per snapshot:

```
rootflags=subvol=.snapshots/N/snapshot snapboot.snapshot=N
```

The base UKI produced by dracut is **never modified or duplicated**. Each addon
is ~1–2 KB regardless of kernel size.

## Requirements

| Dependency | Notes |
|---|---|
| `objcopy` (binutils) | For addon `.efi` generation |
| `snapper` | Config named `root`, watching `/` |
| `btrfs-progs` | For rollback |
| systemd-boot ≥ v253 | For addon support |
| ESP at `/boot` | Configurable in `/etc/snapboot/snapboot.conf` |

## Installation

```bash
git clone https://github.com/n70n10/snapboot
cd snapboot
sudo make install        # builds, installs binary, runs 'snapboot install'
sudo snapboot sync       # generate addons for all existing snapshots
sudo snapboot list       # verify
```

`snapboot install` creates:
- `/etc/snapboot/snapboot.conf` — config file (if not already present)
- `/boot/EFI/Linux/addons/` — addon directory on ESP
- `/var/lib/snapboot/db.json` — state database
- `/etc/snapper/hooks/post-create.d/snapboot` — snapper create hook
- `/etc/snapper/hooks/post-delete.d/snapboot` — snapper delete hook
- `/etc/pacman.d/hooks/99-snapboot.hook` — pacman post-transaction hook

## Automation

After install, everything is hands-off:

| Event | Trigger | Action |
|---|---|---|
| Snapshot created | snapper post-create hook | `snapboot sync --snapshot N` |
| Snapshot deleted | snapper post-delete hook | `snapboot clean` |
| Kernel/dracut upgraded | pacman post-transaction hook | `snapboot sync` (full) |

## Commands

```
snapboot sync [--snapshot N]   Create/update addon .efi (all snapshots, or just N)
snapboot list                  List snapshots with boot addon status
snapboot rollback <N>          Promote snapshot N to new root (btrfs in-place swap)
snapboot clean                 Remove orphaned addons for deleted snapshots
snapboot install               Install hooks, create directories and config
snapboot uninstall             Remove all hooks and generated addon files
snapboot version               Print version
```

Global flag: `--config PATH` (default `/etc/snapboot/snapboot.conf`)

## snapboot list — status values

| Status | Meaning |
|---|---|
| `ok` | Addon exists, snapshot live, kernel current |
| `not-synced` | Snapshot exists but no addon generated yet |
| `stale-kernel` | Addon exists but kernel was upgraded — run `sync` |
| `file-missing` | DB record exists but addon file not found on ESP |
| `orphaned` | Addon exists but snapshot has been deleted — run `clean` |

## Rollback

Booting a snapshot from the menu gives a **read-only** view (snapper snapshots
are read-only btrfs subvolumes). To permanently roll back to that state:

```bash
sudo snapboot rollback <N>
# reboot
```

This:
1. Renames current `@` → `@snapboot-backup-<timestamp>` (preserved on pool)
2. Creates a new read-write `@` from `.snapshots/N/snapshot`
3. Leaves all snapper snapshots and addon files untouched

Reboot required. Delete the old backup when satisfied:

```bash
sudo mount -o subvolid=5 /dev/sdX /mnt
sudo btrfs subvolume delete /mnt/@snapboot-backup-<timestamp>
sudo umount /mnt
```

## Configuration

`/etc/snapboot/snapboot.conf`:

```ini
# Mount point of the EFI System Partition
ESP=/boot

# snapper configuration name to track
SNAPPER_CONFIG=root

# Path to the snapboot state database
DB_PATH=/var/lib/snapboot/db.json
```

## File layout

```
/boot/EFI/Linux/
├── arch-linux.efi              ← base UKI (dracut, untouched)
└── addons/
    ├── snapboot-12.efi         ← cmdline addon for snapshot 12
    ├── snapboot-13.efi
    └── ...

/var/lib/snapboot/
└── db.json                     ← state (snapshot ID → addon path, hashes)

/etc/snapboot/
└── snapboot.conf

/etc/snapper/hooks/
├── post-create.d/snapboot
└── post-delete.d/snapboot

/etc/pacman.d/hooks/
└── 99-snapboot.hook
```

## Caveats

- **Read-only snapshots**: booting a snapshot mounts it read-only. Writes fail.
  Use `rollback` to make it permanent and writable.
- **Cmdline derivation**: addon cmdlines are built from `/proc/cmdline` at sync
  time. If you change kernel parameters, run `snapboot sync` to regenerate.
- **Cache requirement**: rollback needs the btrfs top-level (subvolid=5) to be
  mountable from the running system.
- **Pacman hook targets**: the hook watches `linux`, `linux-lts`, `linux-zen`,
  `linux-hardened`, and `dracut`. Edit `/etc/pacman.d/hooks/99-snapboot.hook`
  to add other kernels (e.g. `linux-cachyos`).
