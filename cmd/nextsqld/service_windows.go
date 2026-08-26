//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
)

const windowsServiceName = "NextSQL"

type windowsService struct{}

func runAsWindowsService() (bool, error) {
	inService, err := svc.IsWindowsService()
	if err != nil || !inService {
		return false, err
	}
	redirectServiceLogs()
	return true, svc.Run(windowsServiceName, &windowsService{})
}

func redirectServiceLogs() {
	dir := filepath.Join(os.Getenv("PROGRAMDATA"), "NextSQL", "logs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "nextsqld.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	os.Stdout = f
	os.Stderr = f
}

func (windowsService) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	serviceStop = stop

	errCh := make(chan error, 1)
	go func() { errCh <- run() }()

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "nextsqld: %v\n", err)
			return true, 1
		}
		return false, 0
	case <-timer.C:
		changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	}

	for {
		select {
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "nextsqld: %v\n", err)
				return true, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(stop)
				select {
				case err := <-errCh:
					if err != nil {
						fmt.Fprintf(os.Stderr, "nextsqld: %v\n", err)
						return true, 1
					}
				case <-time.After(30 * time.Second):
					return true, 1
				}
				return false, 0
			}
		}
	}
}
