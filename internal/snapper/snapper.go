package snapper

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Snapshot represents a single snapper snapshot.
type Snapshot struct {
	ID          int
	Type        string // single, pre, post
	Date        string
	Description string
	SubvolPath  string // absolute path: /.snapshots/N/snapshot
}

// xmlSnapshotList is the root element returned by `snapper --xmlout list`.
// The outer tag is <snapshots>, each child is <snapshot>.
type xmlSnapshotList struct {
	XMLName   xml.Name      `xml:"snapshots"`
	Snapshots []xmlSnapshot `xml:"snapshot"`
}

type xmlSnapshot struct {
	Number      int    `xml:"num"`
	Type        string `xml:"type"`
	Date        string `xml:"date"`
	Description string `xml:"description"`
}

// List returns all snapshots for the given snapper config, excluding snapshot 0
// (which represents the currently running system, not a real snapshot).
func List(config string) ([]Snapshot, error) {
	cmd := exec.Command("snapper", "-c", config, "--xmlout", "list")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("snapper list failed: %w", err)
	}

	var raw xmlSnapshotList
	if err := xml.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("snapper xml parse failed: %w", err)
	}

	var snapshots []Snapshot
	for _, s := range raw.Snapshots {
		if s.Number == 0 {
			continue
		}
		subvol := filepath.Join("/.snapshots", strconv.Itoa(s.Number), "snapshot")
		snapshots = append(snapshots, Snapshot{
			ID:          s.Number,
			Type:        s.Type,
			Date:        s.Date,
			Description: strings.TrimSpace(s.Description),
			SubvolPath:  subvol,
		})
	}
	return snapshots, nil
}

// GetSubvolPath returns the absolute subvol path for a specific snapshot ID.
// This is a pure path construction — safe to call without snapper installed.
func GetSubvolPath(id int) string {
	return filepath.Join("/.snapshots", strconv.Itoa(id), "snapshot")
}

// Exists returns true if the snapshot directory exists on disk.
func Exists(id int) bool {
	_, err := os.Stat(GetSubvolPath(id))
	return err == nil
}
