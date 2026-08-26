package upgrade

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/integrity"
	"github.com/bzync/nextsql/internal/undo"
	"github.com/bzync/nextsql/internal/wal"
)

const (
	magicPage    uint32 = 0x4C51534E // NSQL
	magicWALCtrl uint32 = 0x4357534E // NSWC
	magicUNDOCtl uint32 = 0x4355534E // NSUC

	sbOffVersion  = 4
	sbOffLogical  = 40
	sbOffPhysical = 44
	sbOffCipher   = 48
	sbOffEnvelope = 50
	sbOffNextPage = 56
	sbOffCreated  = 88
	sbOffCheckLSN = 116
	sbOffRedoLSN  = 124
	sbOffChecksum = 252
)

// FileReport is one on-disk encoding observed under a data directory.
type FileReport struct {
	Family   Family
	Path     string
	Present  bool
	Version  uint16
	MagicOK  bool
	Checksum bool
	Compat   bool
	Size     int64
	Detail   string
	Err      string
}

// Report is a diagnose snapshot of a data directory. It does not need
// the root unlock key: it only reads plaintext headers.
type Report struct {
	DataDir    string
	DataFile   string
	Identity   format.Identity
	HasIdent   bool
	PageSize   uint32
	PhysSize   uint32
	Cipher     format.CipherSuite
	Envelope   uint16
	NextPage   format.PageID
	CheckLSN   format.LSN
	RedoLSN    format.LSN
	Created    int64
	WALNext    format.LSN
	WALDur     format.LSN
	WALCheck   format.LSN
	Keystore   bool
	AuthFile   bool
	ACLFile    bool
	Isolated   int
	HasIsolate bool
	Files      []FileReport
	OK         bool
}

// Inspect reads plaintext headers under dataDir and checks them against
// this binary's compatibility catalog. Missing optional files are
// reported, not treated as corruption.
func Inspect(dataDir string) (Report, error) {
	if dataDir == "" {
		return Report{}, nerr.New(nerr.InvalidArgument, "upgrade.Inspect", "data-dir is required")
	}
	st, err := os.Stat(dataDir)
	if err != nil {
		return Report{}, nerr.Wrap(nerr.IO, "upgrade.Inspect", "stat data-dir", err)
	}
	if !st.IsDir() {
		return Report{}, nerr.New(nerr.InvalidArgument, "upgrade.Inspect", "data-dir is not a directory")
	}
	r := Report{
		DataDir:  dataDir,
		DataFile: filepath.Join(dataDir, config.DataFileName),
		OK:       true,
	}
	r.Files = append(r.Files, inspectSuperblock(&r))
	r.Files = append(r.Files, inspectWALCtrl(&r))
	r.Files = append(r.Files, inspectUNDOCtrl(&r))
	r.Keystore = fileExists(cryptoKeystore(r.DataFile))
	r.AuthFile = fileExists(filepath.Join(dataDir, config.AuthFileName))
	r.ACLFile = fileExists(filepath.Join(dataDir, config.ACLFileName))
	if n, present, ierr := integrity.CountFile(integrity.PathFor(r.DataFile)); ierr == nil {
		r.Isolated = n
		r.HasIsolate = present
	}
	for _, f := range r.Files {
		if f.Family == FamilyPage && (!f.Present || f.Err != "") {
			r.OK = false
		}
		if f.Present && (!f.Compat || !f.MagicOK || f.Err != "") {
			r.OK = false
		}
	}
	return r, nil
}

func cryptoKeystore(dbPath string) string { return dbPath + ".keys" }

func inspectSuperblock(r *Report) FileReport {
	fr := FileReport{Family: FamilyPage, Path: r.DataFile}
	st, err := os.Stat(r.DataFile)
	if err != nil {
		if os.IsNotExist(err) {
			fr.Err = "missing data file"
			return fr
		}
		fr.Err = err.Error()
		return fr
	}
	fr.Present = true
	fr.Size = st.Size()
	raw, err := readPrefix(r.DataFile, format.SuperblockSize)
	if err != nil {
		fr.Err = err.Error()
		return fr
	}
	if encoding.U32(raw, 0) != magicPage {
		fr.Err = "bad file magic"
		return fr
	}
	fr.MagicOK = true
	fr.Version = encoding.U16(raw, sbOffVersion)
	fr.Compat = Compatible(FamilyPage, fr.Version)
	if err := checksum.Verify(raw[:format.SuperblockSize], sbOffChecksum); err != nil {
		fr.Err = "superblock checksum mismatch"
	} else {
		fr.Checksum = true
	}
	copy(r.Identity.Database[:], raw[8:24])
	copy(r.Identity.File[:], raw[24:40])
	r.HasIdent = true
	r.PageSize = encoding.U32(raw, sbOffLogical)
	r.PhysSize = encoding.U32(raw, sbOffPhysical)
	r.Cipher = format.CipherSuite(encoding.U16(raw, sbOffCipher))
	r.Envelope = encoding.U16(raw, sbOffEnvelope)
	r.NextPage = format.PageID(encoding.U64(raw, sbOffNextPage))
	r.Created = int64(encoding.U64(raw, sbOffCreated))
	r.CheckLSN = format.LSN(encoding.U64(raw, sbOffCheckLSN))
	r.RedoLSN = format.LSN(encoding.U64(raw, sbOffRedoLSN))
	if !Compatible(FamilyEnvelope, r.Envelope) {
		fr.Compat = false
		if fr.Err == "" {
			fr.Err = "unsupported envelope version"
		}
	}
	if r.PageSize != format.LogicalPageSize || r.PhysSize != format.PhysicalPageSize {
		fr.Compat = false
		if fr.Err == "" {
			fr.Err = "unexpected page size"
		}
	}
	fr.Detail = r.Identity.DatabaseString()
	return fr
}

func inspectWALCtrl(r *Report) FileReport {
	path := filepath.Join(wal.DirFor(r.DataFile), "control")
	fr := FileReport{Family: FamilyWALCtrl, Path: path}
	raw, err := readPrefix(path, 104)
	if err != nil {
		if os.IsNotExist(err) {
			return fr
		}
		fr.Err = err.Error()
		return fr
	}
	fr.Present = true
	fr.Size = fileSize(path)
	if encoding.U32(raw, 0) != magicWALCtrl {
		fr.Err = "bad WAL control magic"
		return fr
	}
	fr.MagicOK = true
	fr.Version = encoding.U16(raw, 4)
	fr.Compat = Compatible(FamilyWALCtrl, fr.Version)
	if err := checksum.Verify(raw[:104], 100); err != nil {
		fr.Err = "WAL control checksum mismatch"
	} else {
		fr.Checksum = true
	}
	r.WALNext = format.LSN(encoding.U64(raw, 40))
	r.WALDur = format.LSN(encoding.U64(raw, 48))
	r.WALCheck = format.LSN(encoding.U64(raw, 56))
	return fr
}

func inspectUNDOCtrl(r *Report) FileReport {
	path := filepath.Join(undo.DirFor(r.DataFile), "control")
	fr := FileReport{Family: FamilyUNDOCtrl, Path: path}
	raw, err := readPrefix(path, 72)
	if err != nil {
		if os.IsNotExist(err) {
			return fr
		}
		fr.Err = err.Error()
		return fr
	}
	fr.Present = true
	fr.Size = fileSize(path)
	if encoding.U32(raw, 0) != magicUNDOCtl {
		fr.Err = "bad UNDO control magic"
		return fr
	}
	fr.MagicOK = true
	fr.Version = encoding.U16(raw, 4)
	fr.Compat = Compatible(FamilyUNDOCtrl, fr.Version)
	if err := checksum.Verify(raw[:72], 68); err != nil {
		fr.Err = "UNDO control checksum mismatch"
	} else {
		fr.Checksum = true
	}
	return fr
}

func readPrefix(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	got, err := f.Read(buf)
	if err != nil && got == 0 {
		return nil, err
	}
	if got < n {
		return buf[:got], nerr.New(nerr.InvalidFormat, "upgrade.Inspect", "truncated header")
	}
	return buf, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// WriteReport prints a diagnose report. It never includes keys.
func WriteReport(w io.Writer, r Report) {
	status := "ok"
	if !r.OK {
		status = "incompatible"
	}
	fmt.Fprintf(w, "data_dir %s\nstatus %s\n", r.DataDir, status)
	if r.HasIdent {
		fmt.Fprintf(w, "database %s\nfile %s\n", r.Identity.DatabaseString(), r.Identity.FileString())
	}
	if r.PageSize != 0 {
		fmt.Fprintf(w, "page_size %d\nphysical_page %d\ncipher %s\nenvelope %d\nnext_page %d\n",
			r.PageSize, r.PhysSize, r.Cipher.String(), r.Envelope, r.NextPage)
	}
	if r.Created != 0 {
		fmt.Fprintf(w, "created %s\n", time.Unix(0, r.Created).UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(w, "checkpoint_lsn %d\nredo_lsn %d\nwal_next_lsn %d\nwal_durable_lsn %d\nwal_checkpoint_lsn %d\n",
		r.CheckLSN, r.RedoLSN, r.WALNext, r.WALDur, r.WALCheck)
	fmt.Fprintf(w, "keystore %t\nauth_file %t\nacl_file %t\nisolated_pages %d\n", r.Keystore, r.AuthFile, r.ACLFile, r.Isolated)
	fmt.Fprintf(w, "\ncompatibility catalog (this binary reads Min..Max):\n")
	for _, s := range Catalog() {
		fmt.Fprintf(w, "  %-14s magic=%-4s current=%d min=%d max=%d  %s\n",
			s.Family, s.Magic, s.Current, s.MinReadable, s.MaxReadable, s.Notes)
	}
	fmt.Fprintf(w, "\non-disk families:\n")
	for _, f := range r.Files {
		if !f.Present {
			fmt.Fprintf(w, "  %-14s missing\n", f.Family)
			continue
		}
		ok := "ok"
		if !f.Compat || !f.MagicOK || f.Err != "" {
			ok = "fail"
		}
		fmt.Fprintf(w, "  %-14s v%d %s checksum=%t size=%d %s\n",
			f.Family, f.Version, ok, f.Checksum, f.Size, f.Err)
	}
}
