package snapper

import (
	"bytes"
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

// colSep is the Unicode box-drawing vertical bar snapper uses as a column
// separator: U+2502 BOX DRAWINGS LIGHT VERTICAL, surrounded by spaces.
const colSep = " \u2502 "

// List returns all snapshots for the given snapper config, excluding snapshot 0.
func List(config string) ([]Snapshot, error) {
	out, err := runSnapper(config)
	if err != nil {
		return nil, err
	}
	return parseSnapperList(out)
}

// runSnapper executes `snapper -c <config> [--no-dbus] list`.
// Tries --no-dbus first, falls back without it.
func runSnapper(config string) ([]byte, error) {
	for _, noDBus := range []bool{true, false} {
		args := []string{"-c", config}
		if noDBus {
			args = append(args, "--no-dbus")
		}
		args = append(args, "list")

		var stderr bytes.Buffer
		cmd := exec.Command("snapper", args...)
		cmd.Stderr = &stderr

		out, err := cmd.Output()
		if err != nil {
			if noDBus {
				continue
			}
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = "(no stderr output)"
			}
			return nil, fmt.Errorf("snapper list failed: %w\nstderr: %s", err, errMsg)
		}
		return out, nil
	}
	return nil, fmt.Errorf("snapper list: all invocations failed")
}

// parseSnapperList parses the Unicode table output of `snapper list`.
// Columns are separated by " │ " (U+2502). The separator row uses "─" and "┼".
func parseSnapperList(output []byte) ([]Snapshot, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// Find the header line: splits on colSep and contains a "#" field.
	headerIdx := -1
	for i, line := range lines {
		fields := strings.Split(line, colSep)
		for _, f := range fields {
			if strings.TrimSpace(f) == "#" {
				headerIdx = i
				break
			}
		}
		if headerIdx >= 0 {
			break
		}
	}
	if headerIdx < 0 {
		preview := strings.TrimSpace(string(output))
		if len(preview) > 400 {
			preview = preview[:400] + "..."
		}
		return nil, fmt.Errorf("snapper list: cannot find header row\nraw output:\n%s", preview)
	}

	// Map column name → split index.
	headerFields := strings.Split(lines[headerIdx], colSep)
	colIdx := make(map[string]int, len(headerFields))
	for i, f := range headerFields {
		colIdx[strings.TrimSpace(f)] = i
	}

	idxNum  := colIdx["#"]
	idxType := colIdx["Type"]
	idxDate := colIdx["Date"]
	idxDesc := colIdx["Description"]

	var snapshots []Snapshot
	for _, line := range lines[headerIdx+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isSeparator(trimmed) {
			continue
		}

		fields := strings.Split(line, colSep)

		numStr := strings.TrimSpace(fieldAt(fields, idxNum))
		id, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		if id == 0 {
			continue
		}

		subvol := filepath.Join("/.snapshots", strconv.Itoa(id), "snapshot")
		snapshots = append(snapshots, Snapshot{
			ID:          id,
			Type:        strings.TrimSpace(fieldAt(fields, idxType)),
			Date:        strings.TrimSpace(fieldAt(fields, idxDate)),
			Description: strings.TrimSpace(fieldAt(fields, idxDesc)),
			SubvolPath:  subvol,
		})
	}
	return snapshots, nil
}

// isSeparator returns true for snapper's Unicode separator rows (─, ┼, spaces).
func isSeparator(line string) bool {
	for _, c := range line {
		if c != '─' && c != '┼' && c != ' ' {
			return false
		}
	}
	return true
}

func fieldAt(fields []string, i int) string {
	if i < 0 || i >= len(fields) {
		return ""
	}
	return fields[i]
}

// GetSubvolPath returns the absolute subvol path for a specific snapshot ID.
func GetSubvolPath(id int) string {
	return filepath.Join("/.snapshots", strconv.Itoa(id), "snapshot")
}

// Exists returns true if the snapshot directory exists on disk.
func Exists(id int) bool {
	_, err := os.Stat(GetSubvolPath(id))
	return err == nil
}
