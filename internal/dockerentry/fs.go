package dockerentry

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// relocateDirContents moves each direct child of src into dst. Rename is
// used when src and dst share a filesystem; a copy-then-remove fallback
// covers the container case where mktemp is on /tmp and --data-dir is a
// volume (GNU mv does the same on EXDEV).
func relocateDirContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if err := os.Rename(from, to); err == nil {
			continue
		}
		info, err := os.Lstat(from)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(from)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, to); err != nil {
				return err
			}
		} else if info.IsDir() {
			if err := os.MkdirAll(to, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyTree(from, to); err != nil {
				return err
			}
		} else {
			if err := copyFile(from, to, info.Mode().Perm()); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(from); err != nil {
			return err
		}
	}
	return nil
}
