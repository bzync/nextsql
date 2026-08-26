// Package migrate implements timestamped SQL migration files, checksums, validate, create, and apply.
package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/version"
)

const (
	// VersionLayout is the UTC timestamp used in migration filenames.
	VersionLayout = "20060102150405"

	// MaxStatementsPerFile is the C.3 cap before any statement is sent.
	MaxStatementsPerFile = 32

	// MaxStatementsPerUp is the C.3 cap for one migrate up invocation.
	MaxStatementsPerUp = 4096

	// MaxCreateAttempts is how many +1s timestamp retries create will try.
	MaxCreateAttempts = 60

	maxSlugLen     = 64
	forcedChecksum = "forced"
)

var (
	// ErrDirty is returned by apply commands when a history row is dirty.
	ErrDirty = withSentinel(nerr.New(nerr.Conflict, "migrate", "database is dirty"), cli.ErrDirty)
	// ErrChecksum is returned when an applied file no longer matches history.
	ErrChecksum = withSentinel(nerr.New(nerr.InvalidFormat, "migrate", "migration checksum mismatch"), cli.ErrChecksum)

	fileNameRE = regexp.MustCompile(`^(\d{14})_([a-z0-9_]+)\.(up|down)\.sql$`)
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
)

type joinedError struct {
	err   error
	extra error
}

func (e *joinedError) Error() string { return e.err.Error() }
func (e *joinedError) Unwrap() []error {
	return []error{e.err, e.extra}
}

func withSentinel(err, sentinel error) error {
	if err == nil {
		return nil
	}
	return &joinedError{err: err, extra: sentinel}
}

// AsValidation marks a file/planning error so cli.Code returns exit 6.
func AsValidation(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cli.ErrDirty) || errors.Is(err, cli.ErrChecksum) || errors.Is(err, cli.ErrValidation) || errors.Is(err, cli.ErrApply) {
		return err
	}
	return withSentinel(err, cli.ErrValidation)
}

// AsApply marks an engine error from apply so cli.Code returns exit 5,
// including nerr.InvalidArgument that would otherwise be usage (exit 1).
func AsApply(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cli.ErrDirty) || errors.Is(err, cli.ErrChecksum) || errors.Is(err, cli.ErrValidation) || errors.Is(err, cli.ErrApply) {
		return err
	}
	return withSentinel(err, cli.ErrApply)
}

// File is one on-disk migration file that matches C.1.
type File struct {
	Path      string
	Version   string
	Name      string
	Direction string
	Checksum  string
}

// Migration is an up file and an optional down pair sharing a version.
type Migration struct {
	Version string
	Name    string
	Up      File
	Down    *File
}

// ParseFileName reports whether name matches C.1.
func ParseFileName(name string) (File, error) {
	m := fileNameRE.FindStringSubmatch(name)
	if m == nil {
		return File{}, nerr.New(nerr.InvalidArgument, "migrate", "invalid filename "+name)
	}
	return File{Version: m[1], Name: m[2], Direction: m[3]}, nil
}

// Checksum is lowercase hex SHA-256 after C.4 normalization.
func Checksum(b []byte) string {
	sum := sha256.Sum256(normalize(b))
	return hex.EncodeToString(sum[:])
}

func normalize(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte{'\n'})
	if bytes.HasSuffix(b, utf8BOM) {
		b = b[:len(b)-len(utf8BOM)]
	}
	return b
}

func parseSource(b []byte) string {
	if bytes.HasPrefix(b, utf8BOM) {
		b = b[len(utf8BOM):]
	}
	return string(b)
}

// Slug lowercases name, maps each non-[a-z0-9]+ run to _, trims, and caps at 64.
func Slug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevUnderscore := false
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	s := strings.Trim(b.String(), "_")
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "_")
	}
	return s
}

// Create writes empty paired up/down files for NAME under dir.
func Create(dir, name string) (upPath, downPath string, err error) {
	return createAt(dir, name, time.Now().UTC())
}

func createAt(dir, name string, now time.Time) (string, string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", "", nerr.New(nerr.InvalidArgument, "migrate", "migrations directory is required")
	}
	slug := Slug(name)
	if slug == "" {
		return "", "", nerr.New(nerr.InvalidArgument, "migrate", "name slugs to empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", nerr.Wrap(nerr.IO, "migrate", "mkdir", err)
	}
	now = now.UTC()
	for i := 0; i < MaxCreateAttempts; i++ {
		ver := now.Add(time.Duration(i) * time.Second).Format(VersionLayout)
		taken, err := versionTaken(dir, ver)
		if err != nil {
			return "", "", err
		}
		if taken {
			continue
		}
		upPath := filepath.Join(dir, ver+"_"+slug+".up.sql")
		downPath := filepath.Join(dir, ver+"_"+slug+".down.sql")
		if err := writeFileExcl(upPath, fileHeader("up", ver, slug)); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", "", nerr.Wrap(nerr.IO, "migrate", "write", err)
		}
		if err := writeFileExcl(downPath, fileHeader("down", ver, slug)); err != nil {
			_ = os.Remove(upPath)
			if os.IsExist(err) {
				continue
			}
			return "", "", nerr.Wrap(nerr.IO, "migrate", "write", err)
		}
		return upPath, downPath, nil
	}
	return "", "", nerr.New(nerr.AlreadyExists, "migrate", "could not allocate a free timestamp")
}

func fileHeader(direction, ver, slug string) string {
	return "-- migrate:" + direction + " " + ver + " " + slug + "\n" +
		"-- NextSQL " + version.String + ": one statement per request; this file is split on ';'.\n" +
		"-- Do not include BEGIN/COMMIT/ROLLBACK.\n"
}

func writeFileExcl(path, body string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func versionTaken(dir, ver string) (bool, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false, nerr.Wrap(nerr.IO, "migrate", "read dir", err)
	}
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		f, err := ParseFileName(e.Name())
		if err != nil {
			continue
		}
		if f.Version == ver {
			return true, nil
		}
	}
	return false, nil
}

func readBody(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "migrate", "read", err)
	}
	if !utf8.Valid(body) {
		return nil, nerr.New(nerr.InvalidFormat, "migrate", filepath.Base(path)+": not valid UTF-8")
	}
	return body, nil
}

// versionFileMeta returns the slug and C.4 checksum of <version>_*.up.sql
// without validating sibling files. Missing or unreadable files use "forced".
func versionFileMeta(dir, version string) (name, sum string) {
	name, sum = forcedChecksum, forcedChecksum
	ents, err := os.ReadDir(dir)
	if err != nil {
		return name, sum
	}
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		f, err := ParseFileName(e.Name())
		if err != nil || f.Version != version || f.Direction != "up" {
			continue
		}
		name = f.Name
		body, err := readBody(filepath.Join(dir, e.Name()))
		if err != nil {
			return name, forcedChecksum
		}
		return name, Checksum(body)
	}
	return name, sum
}
