package supervisorstream_test

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestMain doubles as the fake `yakos dispatch` the wrapper tests run: with
// SS_HELPER set the test binary acts as the child process instead of running
// tests, so the tests need no shell and run on Windows too.
//
//	SS_HELPER=sleep   sleep SS_SLEEP_MS, after starting a grandchild that
//	                  sleeps 5 minutes and records its pid in SS_PIDFILE
//	SS_HELPER=run     append the task argument to SS_RUNS, sleep SS_SLEEP_MS
//	SS_HELPER=runfail record a run in SS_RUNS, sleep SS_SLEEP_MS, exit 1
//	SS_HELPER=limit   print an account session-limit message, exit 1
//	SS_HELPER=grand   sleep 5 minutes (the grandchild)
func TestMain(m *testing.M) {
	if mode := os.Getenv("SS_HELPER"); mode != "" {
		helperMain(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperMain(mode string) {
	ms, _ := strconv.Atoi(os.Getenv("SS_SLEEP_MS"))
	switch mode {
	case "grand":
		time.Sleep(5 * time.Minute)
	case "sleep":
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), "SS_HELPER=grand")
		if err := c.Start(); err == nil {
			_ = os.WriteFile(os.Getenv("SS_PIDFILE"), []byte(strconv.Itoa(c.Process.Pid)), 0o644)
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
	case "run":
		f, _ := os.OpenFile(os.Getenv("SS_RUNS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		task := ""
		if len(os.Args) > 3 {
			task = os.Args[3]
		}
		fmt.Fprintf(f, "run %d %q\n", time.Now().UnixMilli(), task)
		_ = f.Close()
		time.Sleep(time.Duration(ms) * time.Millisecond)
	case "runfail":
		f, _ := os.OpenFile(os.Getenv("SS_RUNS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, "run %d\n", time.Now().UnixMilli())
		_ = f.Close()
		time.Sleep(time.Duration(ms) * time.Millisecond)
		os.Exit(1)
	case "limit":
		time.Sleep(time.Duration(ms) * time.Millisecond)
		fmt.Println("You've hit your session limit · resets 7:30pm (America/New_York)")
		os.Exit(1)
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscallZero()) == nil
}
