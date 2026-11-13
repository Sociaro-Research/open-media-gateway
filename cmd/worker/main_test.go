package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerCLIStartsOnlyWithoutArguments(t *testing.T) {
	sentinel := errors.New("started")
	for _, args := range [][]string{nil, {"--version"}, {"help"}, {"typo"}, {"--version", "extra"}} {
		started := false
		err := executeCLI(args, io.Discard, func() error { started = true; return sentinel })
		if started != (len(args) == 0) || errors.Is(err, sentinel) != (len(args) == 0) {
			t.Fatalf("args=%v started=%v err=%v", args, started, err)
		}
	}
}

func TestWorkerCLIProcessChild(t *testing.T) {
	if os.Getenv("WORKER_CLI_TEST_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	os.Args = append([]string{"worker"}, strings.Fields(os.Getenv("WORKER_CLI_TEST_ARGS"))...)
	main()
}

// Run the actual main with complete service configuration pointing at a
// tripwire: help, version and wrong words must exit before any DB or Redis dial.
func TestWorkerCLIProcessNoServiceAccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connections atomic.Int32
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			connections.Add(1)
			c.Close()
		}
	}()
	defer func() { ln.Close(); <-stopped }()
	for _, tc := range []struct {
		args   string
		ok     bool
		output string
	}{
		{"--version", true, "worker "}, {"version", true, "worker "},
		{"--help", true, "Usage: worker"}, {"-h", true, "Usage: worker"}, {"help", true, "Usage: worker"},
		{"wrong-word", false, "unknown command"}, {"--unknown", false, "unknown command"},
		{"--version extra", false, "unexpected arguments"}, {"--help extra", false, "unexpected arguments"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerCLIProcessChild$")
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"WORKER_CLI_TEST_CHILD=1", "WORKER_CLI_TEST_ARGS=" + tc.args, "DATABASE_URL=postgres://synthetic:synthetic@" + ln.Addr().String() + "/isolated?sslmode=disable", "REDIS_URL=redis://" + ln.Addr().String(), "MIGRATE=auto", "CLIENT_API_KEY=synthetic-cli", "WEBHOOK_SIGNING_SECRET=synthetic-cli", "MEDIA_URL_SIGNING_SECRET=synthetic-cli"}
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil || (err == nil) != tc.ok || !strings.Contains(string(out), tc.output) {
				t.Fatalf("err=%v timeout=%v output=%s", err, ctx.Err(), out)
			}
			if got := connections.Load(); got != 0 {
				t.Fatalf("unexpected service connections: %d", got)
			}
		})
	}
}
