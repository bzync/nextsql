//go:build !windows

package main

func runAsWindowsService() (bool, error) { return false, nil }
