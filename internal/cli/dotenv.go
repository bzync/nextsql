// Package cli loads client settings from flags, the process environment, and dotenv files.
// It is separate from internal/config, which is the nextsqld key=value loader.
package cli

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

const maxEnvWalk = 16

func parseDotenv(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, val, ok, err := parseDotenvLine(sc.Text())
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, nerr.Wrap(nerr.IO, "cli.ParseDotenv", "read", err)
	}
	return out, nil
}

func parseDotenvLine(line string) (key, value string, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	if rest, found := strings.CutPrefix(line, "export"); found {
		if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
			line = strings.TrimSpace(rest)
		}
	}
	k, v, cut := strings.Cut(line, "=")
	if !cut {
		return "", "", false, nerr.New(nerr.InvalidArgument, "cli.ParseDotenv", "expected KEY=VALUE")
	}
	k = strings.TrimSpace(k)
	if k == "" {
		return "", "", false, nerr.New(nerr.InvalidArgument, "cli.ParseDotenv", "empty key")
	}
	v = strings.TrimSpace(v)
	v, err = unquoteDotenv(v)
	if err != nil {
		return "", "", false, err
	}
	return k, v, true, nil
}

func unquoteDotenv(v string) (string, error) {
	if len(v) < 2 {
		return v, nil
	}
	if v[0] == '"' && v[len(v)-1] == '"' {
		s, err := strconv.Unquote(v)
		if err != nil {
			return "", nerr.New(nerr.InvalidArgument, "cli.ParseDotenv", "invalid quoted value")
		}
		return s, nil
	}
	if v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1], nil
	}
	return v, nil
}

func loadDotenvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "cli.LoadDotenv", "open", err)
	}
	defer f.Close()
	return parseDotenv(f)
}

func discoverEnvFiles(start string) (base, local string) {
	if start == "" {
		return "", ""
	}
	if p := filepath.Join(start, ".env.local"); fileExists(p) {
		local = p
	}
	dir := start
	for i := 0; i < maxEnvWalk; i++ {
		if p := filepath.Join(dir, ".env"); fileExists(p) {
			return p, local
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", local
		}
		dir = parent
	}
	return "", local
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
