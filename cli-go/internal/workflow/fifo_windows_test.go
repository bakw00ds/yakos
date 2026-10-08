//go:build windows

package workflow_test

import "errors"

func mkfifo(string) error { return errors.New("no FIFOs on windows") }
func releaseFifo(string)  {}
