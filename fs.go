package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing/fstest"
)

// DirFS wraps a directory path and implements fs.FS.
type DirFS string

func (d DirFS) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(string(d), name))
}

func (d DirFS) Root() string { return string(d) }

// SerializeOptions configures serialization behavior.
type SerializeOptions struct {
	FS  fs.FS // Filesystem to read/write (use *os.Root or fstest.MapFS)
	VCS VCS   // Optional: VCS for listing files (required for DirFS in jj alternative workspaces)
}

// fsReadFile reads a file from the filesystem.
func fsReadFile(fsys fs.FS, name string) ([]byte, error) {
	return fs.ReadFile(fsys, name)
}

// fsWriteFile writes a file to the filesystem.
func fsWriteFile(fsys fs.FS, name string, data []byte) error {
	switch f := fsys.(type) {
	case fstest.MapFS:
		f[name] = &fstest.MapFile{Data: data}
		return nil
	case DirFS:
		return os.WriteFile(filepath.Join(string(f), name), data, 0644)
	default:
		return fmt.Errorf("unsupported filesystem type %T for writing", fsys)
	}
}

// fsListFiles returns all files to scan for comments.
func fsListFiles(opts SerializeOptions) ([]string, error) {
	switch f := opts.FS.(type) {
	case fstest.MapFS:
		var files []string
		for name := range f {
			if name != prStateFile {
				files = append(files, name)
			}
		}
		sort.Strings(files)
		return files, nil
	case DirFS:
		// Use VCS.ListFiles if available (handles jj alternative workspaces)
		if opts.VCS != nil {
			return opts.VCS.ListFiles()
		}
		// Fallback to git ls-files for backwards compatibility
		cmd := exec.Command("git", "ls-files")
		cmd.Dir = string(f)
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		var files []string
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" {
				files = append(files, line)
			}
		}
		return files, nil
	default:
		return nil, fmt.Errorf("unsupported filesystem type %T for listing", opts.FS)
	}
}
