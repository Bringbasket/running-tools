package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var postgresState = struct {
	sync.RWMutex
	db    *sql.DB
	roots []string
}{}

// ConfigurePostgres makes the existing state API persist JSON documents in
// PostgreSQL. The file path remains the stable logical key for compatibility.
func ConfigurePostgres(db *sql.DB, roots ...string) {
	postgresState.Lock()
	postgresState.db = db
	postgresState.roots = postgresState.roots[:0]
	for _, root := range roots {
		if cleaned := filepath.Clean(strings.TrimSpace(root)); cleaned != "." && cleaned != "" {
			postgresState.roots = append(postgresState.roots, cleaned)
		}
	}
	postgresState.Unlock()
}

func ResetPostgres() {
	postgresState.Lock()
	postgresState.db = nil
	postgresState.roots = nil
	postgresState.Unlock()
}

func stateDB(path string) *sql.DB {
	postgresState.RLock()
	defer postgresState.RUnlock()
	cleaned := filepath.Clean(path)
	for _, root := range postgresState.roots {
		if cleaned == root || strings.HasPrefix(cleaned, root+string(filepath.Separator)) {
			// The host update worker intentionally exchanges small JSON files in
			// data/system. They are a process boundary, not business state.
			relative, err := filepath.Rel(root, cleaned)
			if err == nil && (relative == "system" || strings.HasPrefix(relative, "system"+string(filepath.Separator))) {
				continue
			}
			return postgresState.db
		}
	}
	return nil
}

func ReadJSON(path string, target any) error {
	if db := stateDB(path); db != nil {
		var data []byte
		if err := db.QueryRowContext(context.Background(), `SELECT value::text FROM running_state WHERE state_key = $1`, path).Scan(&data); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read PostgreSQL state %s: %w", path, err)
			}
			// PostgreSQL is the only active store. A missing row may still have a
			// legacy file from an earlier release, so import it once before reading.
			legacy, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if err := json.Unmarshal(legacy, target); err != nil {
				return fmt.Errorf("decode legacy %s: %w", path, err)
			}
			if _, err := db.ExecContext(context.Background(), `INSERT INTO running_state (state_key, value, updated_at) VALUES ($1, $2::jsonb, NOW()) ON CONFLICT (state_key) DO NOTHING`, path, legacy); err != nil {
				return fmt.Errorf("import legacy PostgreSQL state %s: %w", path, err)
			}
			return nil
		}
		if err := json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func WriteJSON(path string, value any, mode os.FileMode) error {
	if db := stateDB(path); db != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode PostgreSQL state: %w", err)
		}
		if _, err := db.ExecContext(context.Background(), `INSERT INTO running_state (state_key, value, updated_at) VALUES ($1, $2::jsonb, NOW()) ON CONFLICT (state_key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, path, data); err != nil {
			return fmt.Errorf("write PostgreSQL state %s: %w", path, err)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set state file permissions: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode state file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync state file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		// Windows cannot replace an existing file with Rename. Keep the normal
		// atomic path on Unix and use a narrow compatibility fallback locally.
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("replace state file: %w", err)
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("replace state file: %w", err)
		}
		if renameErr := os.Rename(temporaryName, path); renameErr != nil {
			return fmt.Errorf("replace state file after remove: %w", renameErr)
		}
	}
	return nil
}

func Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
