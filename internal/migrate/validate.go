package migrate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
)

// Validate lists dir, checks pairing and uniqueness, and parses every statement.
// Hidden files and subdirectories are ignored. No server is required.
func Validate(dir string) ([]Migration, error) {
	migs, err := validateDir(dir)
	return migs, AsValidation(err)
}

func validateDir(dir string) ([]Migration, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nerr.New(nerr.InvalidArgument, "migrate", "migrations directory is required")
	}
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nerr.New(nerr.NotFound, "migrate", "migrations directory not found")
		}
		return nil, nerr.Wrap(nerr.IO, "migrate", "stat", err)
	}
	if !st.IsDir() {
		return nil, nerr.New(nerr.InvalidArgument, "migrate", "migrations path is not a directory")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "migrate", "read dir", err)
	}
	var files []File
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		f, err := ParseFileName(name)
		if err != nil {
			return nil, err
		}
		f.Path = filepath.Join(dir, name)
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Version != files[j].Version {
			return files[i].Version < files[j].Version
		}
		if files[i].Name != files[j].Name {
			return files[i].Name < files[j].Name
		}
		return files[i].Direction < files[j].Direction
	})
	byVer := make(map[string]*Migration, len(files))
	var order []string
	for _, f := range files {
		m, ok := byVer[f.Version]
		if !ok {
			m = &Migration{Version: f.Version, Name: f.Name}
			byVer[f.Version] = m
			order = append(order, f.Version)
		} else if m.Name != f.Name {
			return nil, nerr.New(nerr.InvalidArgument, "migrate", "duplicate version "+f.Version)
		}
		loaded, err := inspectFile(f)
		if err != nil {
			return nil, err
		}
		switch loaded.Direction {
		case "up":
			if m.Up.Path != "" {
				return nil, nerr.New(nerr.InvalidArgument, "migrate", "duplicate version "+f.Version)
			}
			m.Up = loaded
		case "down":
			if m.Down != nil {
				return nil, nerr.New(nerr.InvalidArgument, "migrate", "duplicate version "+f.Version)
			}
			cp := loaded
			m.Down = &cp
		}
	}
	out := make([]Migration, 0, len(order))
	for _, ver := range order {
		m := byVer[ver]
		if m.Up.Path == "" {
			return nil, nerr.New(nerr.InvalidArgument, "migrate", "down without up for "+ver)
		}
		out = append(out, *m)
	}
	return out, nil
}

func inspectFile(f File) (File, error) {
	body, err := readBody(f.Path)
	if err != nil {
		return f, err
	}
	f.Checksum = Checksum(body)
	stmts, err := Split(parseSource(body))
	if err != nil {
		return f, err
	}
	if len(stmts) > MaxStatementsPerFile {
		return f, nerr.New(nerr.InvalidArgument, "migrate", filepath.Base(f.Path)+": more than 32 statements")
	}
	for _, s := range stmts {
		if len(s) > security.MaxSQLBytes {
			return f, nerr.New(nerr.InvalidArgument, "migrate", filepath.Base(f.Path)+": statement exceeds 1 MiB")
		}
		if err := checkStatement(s); err != nil {
			return f, annotateFile(f.Path, err)
		}
	}
	return f, nil
}

func checkStatement(s string) error {
	stmt, err := parser.Parse(s)
	if err != nil {
		if kind := unimplementedKind(s); kind != "" {
			return nerr.New(nerr.InvalidArgument, "migrate", kind+" is not implemented")
		}
		return nerr.Wrap(nerr.Syntax, "migrate", "parse", err)
	}
	return rejectStmt(stmt)
}

func rejectStmt(stmt ast.Stmt) error {
	if ex, ok := stmt.(ast.Explain); ok {
		return rejectStmt(ex.Stmt)
	}
	switch stmt.(type) {
	case ast.Begin, ast.Commit, ast.Rollback:
		return nerr.New(nerr.InvalidArgument, "migrate", "BEGIN/COMMIT/ROLLBACK is not allowed")
	case ast.SetTenant:
		return nerr.New(nerr.InvalidArgument, "migrate", "SET TENANT/RESET TENANT is not allowed")
	case ast.Grant, ast.Revoke, ast.CreateUser, ast.DropUser, ast.CreateRole, ast.DropRole:
		return nerr.New(nerr.InvalidArgument, "migrate", "GRANT/REVOKE and CREATE/DROP USER/ROLE are not allowed")
	default:
		return nil
	}
}

func unimplementedKind(src string) string {
	_ = src
	return ""
}

func annotateFile(path string, err error) error {
	if err == nil {
		return nil
	}
	base := filepath.Base(path)
	if e, ok := err.(*nerr.Error); ok {
		return nerr.New(e.Code, "migrate", base+": "+e.Message)
	}
	return nerr.Wrap(nerr.InvalidArgument, "migrate", base, err)
}
