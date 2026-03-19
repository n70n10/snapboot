package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const DefaultPath = "/var/lib/snapboot/db.json"

// AddonEntry records a generated addon for one snapshot.
type AddonEntry struct {
	SnapshotID  int    `json:"snapshot_id"`
	SubvolPath  string `json:"subvol_path"`  // e.g. .snapshots/5/snapshot
	KernelHash  string `json:"kernel_hash"`  // SHA256 of base UKI used
	BaseUKI     string `json:"base_uki"`     // abs path on ESP
	AddonPath   string `json:"addon_path"`   // abs path on ESP
	CmdlineHash string `json:"cmdline_hash"` // hash of the cmdline string
	Description string `json:"description"`  // from snapper
	CreatedAt   string `json:"created_at"`
}

// DB is the in-memory state database.
type DB struct {
	mu      sync.Mutex
	path    string
	Entries map[int]*AddonEntry // keyed by snapshot ID
}

// jsonDB is the on-disk representation. Decoupled from DB so that unexported
// fields (mu, path) do not interfere with JSON encoding/decoding.
type jsonDB struct {
	Entries map[int]*AddonEntry `json:"entries"`
}

// Load reads the database from disk. Returns an empty DB if the file does
// not exist yet (first run is fine).
func Load(path string) (*DB, error) {
	d := &DB{
		path:    path,
		Entries: make(map[int]*AddonEntry),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return d, nil
		}
		return nil, err
	}
	var jd jsonDB
	if err := json.Unmarshal(data, &jd); err != nil {
		return nil, err
	}
	if jd.Entries != nil {
		d.Entries = jd.Entries
	}
	return d, nil
}

// Save writes the database to disk atomically (temp file + rename).
func (d *DB) Save() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(d.path), 0755); err != nil {
		return err
	}
	jd := jsonDB{Entries: d.Entries}
	data, err := json.MarshalIndent(jd, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// Set stores an entry for the given snapshot ID.
func (d *DB) Set(id int, e *AddonEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Entries[id] = e
}

// Get retrieves the entry for the given snapshot ID.
func (d *DB) Get(id int) (*AddonEntry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.Entries[id]
	return e, ok
}

// Delete removes the entry for the given snapshot ID.
func (d *DB) Delete(id int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.Entries, id)
}

// All returns a copy of all entries safe to iterate while the DB is mutated.
func (d *DB) All() []*AddonEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*AddonEntry, 0, len(d.Entries))
	for _, e := range d.Entries {
		out = append(out, e)
	}
	return out
}
