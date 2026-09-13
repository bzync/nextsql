//go:build windows

package diskspace

// NextSQL's server and tools do not run natively on Windows; run them inside
// WSL 2 (docs/install.md, "Windows (WSL 2)"). Referencing an undefined name
// makes a GOOS=windows build of an engine binary fail with this reason.
var _ = NextSQL_does_not_support_native_Windows__run_it_under_WSL_2
