package migrate

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

//go:embed migrations/*.up.sql
var embeddedMigrations embed.FS

var migrationNamePattern = regexp.MustCompile(`^([0-9]{6})_([a-z][a-z0-9_]*)\.up\.sql$`)

// Migration 是经过命名、排序和 checksum 验证的 immutable SQL 单元。
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

func loadMigrations(filesystem fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(filesystem, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationNamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("migration filename %q must match NNNNNN_name.up.sql", entry.Name())
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration filename %q has an invalid version", entry.Name())
		}
		body, err := fs.ReadFile(filesystem, filepath.ToSlash(filepath.Join("migrations", entry.Name())))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}
		checksum := sha256.Sum256(body)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     matches[2],
			SQL:      string(body),
			Checksum: hex.EncodeToString(checksum[:]),
		})
	}

	sort.Slice(migrations, func(left, right int) bool {
		return migrations[left].Version < migrations[right].Version
	})
	for index, migration := range migrations {
		expectedVersion := int64(index + 1)
		if migration.Version != expectedVersion {
			return nil, fmt.Errorf("migration versions must be contiguous from 000001; expected %06d", expectedVersion)
		}
	}
	return migrations, nil
}
