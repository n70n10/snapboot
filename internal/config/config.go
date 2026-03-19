package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const DefaultPath = "/etc/snapboot/snapboot.conf"

// Config holds all runtime-configurable values for snapboot.
// Values are loaded from the config file; defaults are used for any missing key.
type Config struct {
	// ESP is the mount point of the EFI System Partition.
	ESP string
	// SnapperConfig is the snapper configuration name to watch.
	SnapperConfig string
	// DBPath is the path to the state database JSON file.
	DBPath string
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		ESP:           "/boot",
		SnapperConfig: "root",
		DBPath:        "/var/lib/snapboot/db.json",
	}
}

// Load reads the config file and applies any overrides onto the defaults.
// If the config file does not exist, the defaults are returned as-is.
func Load(path string) (*Config, error) {
	cfg := Default()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("cannot open config %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		// Skip blank lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%s:%d: invalid line (expected KEY=VALUE): %q", path, lineNo, line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip optional inline comments.
		if idx := strings.Index(val, " #"); idx >= 0 {
			val = strings.TrimSpace(val[:idx])
		}

		switch key {
		case "ESP":
			cfg.ESP = val
		case "SNAPPER_CONFIG":
			cfg.SnapperConfig = val
		case "DB_PATH":
			cfg.DBPath = val
		default:
			return nil, fmt.Errorf("%s:%d: unknown key %q", path, lineNo, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DefaultFileContent returns the content written to /etc/snapboot/snapboot.conf
// during `snapboot install`.
func DefaultFileContent() string {
	return `# snapboot configuration
# Lines starting with # are comments. All values are optional;
# the defaults shown here match a standard EndeavourOS/Arch setup.

# Mount point of the EFI System Partition
ESP=/boot

# snapper configuration name to track
SNAPPER_CONFIG=root

# Path to the snapboot state database
DB_PATH=/var/lib/snapboot/db.json
`
}
