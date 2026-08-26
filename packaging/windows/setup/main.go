//go:build windows

// NextSQL Windows installer. The build script appends a zip payload and an
// 16-byte footer (little-endian zip size + magic "NEXTSFX1") to this binary.
package main

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	productName    = "NextSQL"
	serviceName    = "NextSQL"
	publisher      = "bzync"
	payloadMagic   = "NEXTSFX1"
	uninstallKey   = `Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL`
	productKey     = `Software\NextSQL`
)

var version = "dev"

type options struct {
	silent     bool
	uninstall  bool
	removeData bool
	elevated   bool
	installDir string
	dataRoot   string
}

func main() {
	opt := parseArgs(os.Args[1:])
	if err := run(opt); err != nil {
		msg := err.Error()
		_ = os.WriteFile(filepath.Join(os.TempDir(), "nextsql-setup.log"), []byte(msg+"\n"), 0o600)
		if !opt.silent {
			messageBox(productName, msg, windows.MB_OK|windows.MB_ICONERROR)
		} else {
			fmt.Fprintf(os.Stderr, "nextsql-setup: %s\n", msg)
		}
		os.Exit(1)
	}
}

func parseArgs(args []string) options {
	opt := options{}
	fs := flag.NewFlagSet("nextsql-setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opt.silent, "S", false, "silent")
	fs.BoolVar(&opt.silent, "silent", false, "silent")
	fs.BoolVar(&opt.uninstall, "uninstall", false, "uninstall")
	fs.BoolVar(&opt.removeData, "removedata", false, "delete data dir on uninstall")
	fs.BoolVar(&opt.elevated, "elevated", false, "internal")
	fs.StringVar(&opt.installDir, "D", "", "install directory")
	fs.StringVar(&opt.installDir, "dir", "", "install directory")
	_ = fs.Parse(args)
	for _, a := range args {
		switch {
		case a == "/S" || a == "/silent" || strings.EqualFold(a, "/quiet"):
			opt.silent = true
		case strings.EqualFold(a, "/uninstall") || a == "/U":
			opt.uninstall = true
		case strings.EqualFold(a, "/removedata"):
			opt.removeData = true
		case strings.EqualFold(a, "/elevated"):
			opt.elevated = true
		case strings.HasPrefix(a, "/D="):
			opt.installDir = strings.TrimPrefix(a, "/D=")
		}
	}
	if opt.installDir == "" {
		pf := os.Getenv("PROGRAMFILES")
		if pf == "" {
			pf = `C:\Program Files`
		}
		opt.installDir = filepath.Join(pf, productName)
	}
	pd := os.Getenv("PROGRAMDATA")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	opt.dataRoot = filepath.Join(pd, productName)
	return opt
}

func run(opt options) error {
	if !isElevated() {
		return relaunchElevated()
	}
	const idOK = 1
	if opt.uninstall {
		if !opt.silent {
			if messageBox(productName, "Uninstall NextSQL?\n\nData directories and the root unlock key are left in place unless you used /removedata.", windows.MB_OKCANCEL|windows.MB_ICONQUESTION) != idOK {
				return nil
			}
		}
		return uninstall(opt)
	}
	if !opt.silent {
		msg := fmt.Sprintf("Install NextSQL %s to:\n\n%s\n\nData: %s\nKeys: %s\n\nThe Windows service is registered but not started. You must run nextsql init first.",
			version, opt.installDir, filepath.Join(opt.dataRoot, "data"), filepath.Join(opt.dataRoot, "keys", "root.key"))
		if messageBox(productName+" Setup", msg, windows.MB_OKCANCEL|windows.MB_ICONINFORMATION) != idOK {
			return nil
		}
	}
	if err := install(opt); err != nil {
		return err
	}
	if !opt.silent {
		next := fmt.Sprintf("NextSQL %s is installed.\n\nInitialize a data directory, then start the service:\n\n  nextsql init --data-dir \"%s\" --key-file \"%s\" --user app --password-file <password-file>\n  net start NextSQL\n\nKeep the root unlock key off the data volume in production.",
			version,
			filepath.Join(opt.dataRoot, "data"),
			filepath.Join(opt.dataRoot, "keys", "root.key"),
		)
		messageBox(productName+" Setup", next, windows.MB_OK|windows.MB_ICONINFORMATION)
	}
	return nil
}

func install(opt options) error {
	zr, closer, err := openPayload()
	if err != nil {
		return err
	}
	defer closer()

	if err := os.MkdirAll(opt.installDir, 0o755); err != nil {
		return err
	}
	for _, d := range []string{
		filepath.Join(opt.dataRoot, "data"),
		filepath.Join(opt.dataRoot, "keys"),
		filepath.Join(opt.dataRoot, "logs"),
	} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	if err := restrictKeysDir(filepath.Join(opt.dataRoot, "keys")); err != nil {
		return fmt.Errorf("restrict keys directory: %w", err)
	}

	want := map[string]string{
		"nextsql.exe":       filepath.Join(opt.installDir, "nextsql.exe"),
		"nextsqld.exe":      filepath.Join(opt.installDir, "nextsqld.exe"),
		"nextsql-bench.exe": filepath.Join(opt.installDir, "nextsql-bench.exe"),
		"README.txt":        filepath.Join(opt.installDir, "README.txt"),
		"COPYRIGHT":         filepath.Join(opt.installDir, "COPYRIGHT"),
		"uninstall.ps1":     filepath.Join(opt.installDir, "uninstall.ps1"),
		"nextsql.ico":       filepath.Join(opt.installDir, "nextsql.ico"),
		"VERSION":           filepath.Join(opt.installDir, "VERSION"),
		"install.ps1":       filepath.Join(opt.installDir, "install.ps1"),
	}
	for _, f := range zr.File {
		name := filepath.Base(strings.ReplaceAll(f.Name, "\\", "/"))
		dest, ok := want[name]
		if !ok {
			if name == "nextsql.conf" {
				conf := filepath.Join(opt.dataRoot, "nextsql.conf")
				if _, err := os.Stat(conf); err == nil {
					continue
				}
				dest = conf
			} else {
				continue
			}
		}
		if err := extractFile(f, dest); err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
	}
	for _, required := range []string{"nextsql.exe", "nextsqld.exe"} {
		if _, err := os.Stat(filepath.Join(opt.installDir, required)); err != nil {
			return fmt.Errorf("payload missing %s", required)
		}
	}

	self, err := os.Executable()
	if err == nil {
		_ = copyFile(self, filepath.Join(opt.installDir, "uninstall.exe"))
	}

	if err := addToMachinePath(opt.installDir); err != nil {
		return fmt.Errorf("PATH: %w", err)
	}
	if err := writeUninstallRegistry(opt); err != nil {
		return err
	}
	if err := registerService(opt); err != nil {
		return err
	}
	_ = createShortcuts(opt)
	return nil
}

func uninstall(opt options) error {
	_ = exec.Command("sc.exe", "stop", serviceName).Run()
	_ = exec.Command("sc.exe", "delete", serviceName).Run()
	_ = removeFromMachinePath(opt.installDir)
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, productKey)
	programs := filepath.Join(os.Getenv("PROGRAMDATA"), `Microsoft\Windows\Start Menu\Programs`, productName)
	_ = os.RemoveAll(programs)
	_ = os.RemoveAll(opt.installDir)
	if opt.removeData {
		_ = os.RemoveAll(opt.dataRoot)
	}
	return nil
}

func openPayload() (*zip.Reader, func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(exe)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	size := st.Size()
	if size < 16 {
		f.Close()
		return nil, nil, errors.New("installer has no payload")
	}
	footer := make([]byte, 16)
	if _, err := f.ReadAt(footer, size-16); err != nil {
		f.Close()
		return nil, nil, err
	}
	if string(footer[8:]) != payloadMagic {
		f.Close()
		return nil, nil, errors.New("not a NextSQL setup payload (rebuild with scripts/build-windows-installer.sh)")
	}
	zipSize := int64(binary.LittleEndian.Uint64(footer[:8]))
	start := size - 16 - zipSize
	if start < 0 || zipSize < 22 {
		f.Close()
		return nil, nil, errors.New("corrupt installer payload")
	}
	sr := io.NewSectionReader(f, start, zipSize)
	zr, err := zip.NewReader(sr, zipSize)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return zr, func() { f.Close() }, nil
}

func extractFile(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, rc)
	cerr := out.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	return os.Rename(tmp, dest)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

func restrictKeysDir(dir string) error {
	cmd := exec.Command("icacls", dir, "/inheritance:r", "/grant:r", "SYSTEM:(OI)(CI)F", "Administrators:(OI)(CI)F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func registerService(opt options) error {
	bin := fmt.Sprintf(`"%s" --config "%s"`,
		filepath.Join(opt.installDir, "nextsqld.exe"),
		filepath.Join(opt.dataRoot, "nextsql.conf"),
	)
	_ = exec.Command("sc.exe", "stop", serviceName).Run()
	_ = exec.Command("sc.exe", "delete", serviceName).Run()
	cmd := exec.Command("sc.exe", "create", serviceName,
		"binPath=", bin,
		"start=", "demand",
		"DisplayName=", "NextSQL Database Server",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("sc.exe", "description", serviceName, "Encrypted-by-default multimodel database").Run()
	return nil
}

func writeUninstallRegistry(opt options) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	ico := filepath.Join(opt.installDir, "nextsql.ico")
	uninst := fmt.Sprintf(`"%s" /uninstall`, filepath.Join(opt.installDir, "uninstall.exe"))
	_ = k.SetStringValue("DisplayName", productName)
	_ = k.SetStringValue("DisplayVersion", version)
	_ = k.SetStringValue("Publisher", publisher)
	_ = k.SetStringValue("InstallLocation", opt.installDir)
	_ = k.SetStringValue("UninstallString", uninst)
	_ = k.SetStringValue("DisplayIcon", ico)
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	pk, _, err := registry.CreateKey(registry.LOCAL_MACHINE, productKey, registry.ALL_ACCESS)
	if err == nil {
		_ = pk.SetStringValue("InstallDir", opt.installDir)
		pk.Close()
	}
	return nil
}

func addToMachinePath(dir string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	path, _, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	for _, p := range strings.Split(path, ";") {
		if strings.EqualFold(strings.TrimRight(p, `\`), strings.TrimRight(dir, `\`)) {
			broadcastEnvironment()
			return nil
		}
	}
	if path != "" && !strings.HasSuffix(path, ";") {
		path += ";"
	}
	path += dir
	if err := k.SetExpandStringValue("Path", path); err != nil {
		return err
	}
	broadcastEnvironment()
	return nil
}

func removeFromMachinePath(dir string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	path, _, err := k.GetStringValue("Path")
	if err != nil {
		return err
	}
	var keep []string
	for _, p := range strings.Split(path, ";") {
		if p == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(p, `\`), strings.TrimRight(dir, `\`)) {
			continue
		}
		keep = append(keep, p)
	}
	if err := k.SetExpandStringValue("Path", strings.Join(keep, ";")); err != nil {
		return err
	}
	broadcastEnvironment()
	return nil
}

func broadcastEnvironment() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	env, _ := syscall.UTF16PtrFromString("Environment")
	const hwndBroadcast = 0xffff
	const wmWinIniChange = 0x001A
	const smtoAbortIfHung = 0x0002
	_, _, _ = proc.Call(hwndBroadcast, wmWinIniChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, 0)
}

func createShortcuts(opt options) error {
	programs := filepath.Join(os.Getenv("PROGRAMDATA"), `Microsoft\Windows\Start Menu\Programs`, productName)
	if err := os.MkdirAll(programs, 0o755); err != nil {
		return err
	}
	script := fmt.Sprintf(`
$ws = New-Object -ComObject WScript.Shell
$s = $ws.CreateShortcut(%q)
$s.TargetPath = %q
$s.WorkingDirectory = %q
$s.Description = "NextSQL CLI"
$ico = %q
if (Test-Path $ico) { $s.IconLocation = $ico }
$s.Save()
$u = $ws.CreateShortcut(%q)
$u.TargetPath = %q
$u.Arguments = "/uninstall"
$u.WorkingDirectory = %q
$u.Description = "Uninstall NextSQL"
if (Test-Path $ico) { $u.IconLocation = $ico }
$u.Save()
`,
		filepath.Join(programs, "NextSQL CLI.lnk"),
		filepath.Join(opt.installDir, "nextsql.exe"),
		opt.installDir,
		filepath.Join(opt.installDir, "nextsql.ico"),
		filepath.Join(programs, "Uninstall NextSQL.lnk"),
		filepath.Join(opt.installDir, "uninstall.exe"),
		opt.installDir,
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	return cmd.Run()
}

func isElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	args := append([]string{}, os.Args[1:]...)
	has := false
	for _, a := range args {
		if strings.EqualFold(a, "/elevated") || a == "-elevated" {
			has = true
		}
	}
	if !has {
		args = append(args, "/elevated")
	}
	param, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return err
	}
	var cwd *uint16
	show := int32(windows.SW_NORMAL)
	err = windows.ShellExecute(0, verb, file, param, cwd, show)
	if err != nil && !errors.Is(err, windows.ERROR_SUCCESS) {
		// ShellExecute returns a HINSTANCE; x/sys wraps failures as error.
		if errno, ok := err.(syscall.Errno); ok && uintptr(errno) > 32 {
			return nil
		}
		if err.Error() == "The operation completed successfully." {
			return nil
		}
		return err
	}
	return nil
}

func messageBox(caption, text string, flags uint32) int {
	ret, _ := windows.MessageBox(0, windows.StringToUTF16Ptr(text), windows.StringToUTF16Ptr(caption), flags)
	return int(ret)
}
