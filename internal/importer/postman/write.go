package postman

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options control Import.
type Options struct {
	// OutDir receives the collection. It is created if missing.
	OutDir string
	// Force allows writing into a non-empty directory. Existing files with
	// the same names are overwritten; others are left alone.
	Force bool
	// EnvFiles are Postman environment exports to convert alongside.
	EnvFiles []string
}

// Import reads a Postman collection export and writes an Otis collection.
func Import(collectionPath string, opts Options) (*Report, error) {
	data, err := os.ReadFile(collectionPath)
	if err != nil {
		return nil, err
	}
	var envs [][]byte
	for _, p := range opts.EnvFiles {
		e, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		envs = append(envs, e)
	}
	out, err := Plan(data, envs...)
	if err != nil {
		return nil, err
	}
	if err := Write(out, opts.OutDir, opts.Force); err != nil {
		return out.Report, err
	}
	return out.Report, nil
}

// Write puts a planned Output on disk under dir.
func Write(out *Output, dir string, force bool) error {
	if dir == "" {
		return fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !force {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var visible []string
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") || e.Name() == ".order" {
				visible = append(visible, e.Name())
			}
		}
		if len(visible) > 0 {
			sort.Strings(visible)
			return fmt.Errorf("output directory %s is not empty (%s); use --force to write into it", dir, strings.Join(visible, ", "))
		}
	}
	for _, rel := range sortedKeys(out.Files) {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(out.Files[rel]), 0o644); err != nil {
			return err
		}
	}
	return nil
}
