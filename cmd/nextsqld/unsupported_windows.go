//go:build windows

package main

// NextSQL does not run natively on Windows. On a Windows machine, install and
// run it inside WSL 2 (see docs/install.md, "Windows (WSL 2)"). Referencing an
// undefined name makes a GOOS=windows build fail with this reason rather than
// with an unrelated missing platform function.
var _ = NextSQL_does_not_support_native_Windows__run_it_under_WSL_2
