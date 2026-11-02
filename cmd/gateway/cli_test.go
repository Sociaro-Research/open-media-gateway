package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCLIDispatchBoundary(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		call    string
		wantErr bool
	}{
		{nil, "serve", false}, {[]string{"healthcheck"}, "health", false},
		{[]string{"admin", "users", "--search", "synthetic"}, "admin", false},
		{[]string{"--help"}, "", false}, {[]string{"-h"}, "", false}, {[]string{"help"}, "", false},
		{[]string{"--version"}, "", false}, {[]string{"version"}, "", false}, {[]string{"admin", "--help"}, "", false},
		{[]string{"typo"}, "", true}, {[]string{"--unknown"}, "", true},
		{[]string{"healthcheck", "extra"}, "", true}, {[]string{"--version", "extra"}, "", true},
		{[]string{"--help", "extra"}, "", true}, {[]string{"admin"}, "", true}, {[]string{"admin", "typo"}, "", true},
	} {
		t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
			var calls []string
			sentinel := errors.New("handler result")
			h := cliHandlers{serve: func() error { calls = append(calls, "serve"); return sentinel }, health: func() error { calls = append(calls, "health"); return sentinel }, admin: func(args []string) error {
				calls = append(calls, "admin")
				if !reflect.DeepEqual(args, tc.args[1:]) {
					t.Fatal("admin args changed")
				}
				return sentinel
			}}
			var out bytes.Buffer
			err := executeCLI(tc.args, &out, h)
			if tc.call != "" {
				if !reflect.DeepEqual(calls, []string{tc.call}) || !errors.Is(err, sentinel) {
					t.Fatalf("calls=%v err=%v", calls, err)
				}
			} else if len(calls) != 0 || (err != nil) != tc.wantErr {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
			if !tc.wantErr && tc.call == "" && out.Len() == 0 {
				t.Fatal("missing local output")
			}
		})
	}
}

func TestCLIProcessChild(t *testing.T) {
	if os.Getenv("GATEWAY_CLI_TEST_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	os.Args = append([]string{"gateway"}, strings.Split(os.Getenv("GATEWAY_CLI_TEST_ARGS"), "\n")...)
	main()
}

// Run the actual main, not just the parser. Any DB, Redis or health HTTP dial
// reaches this isolated tripwire; there are no inherited credentials or .env.
func TestCLIProcessNoServiceAccess(t *testing.T) {
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
		args   []string
		ok     bool
		output string
	}{
		{[]string{"--version"}, true, "gateway "}, {[]string{"version"}, true, "gateway "},
		{[]string{"--help"}, true, "Usage: gateway"}, {[]string{"-h"}, true, "Usage: gateway"}, {[]string{"help"}, true, "Usage: gateway"},
		{[]string{"admin", "--help"}, true, "usage: gateway admin"},
		{[]string{"wrong-word"}, false, "unknown command"}, {[]string{"--unknown"}, false, "unknown command"},
		{[]string{"healthcheck", "extra"}, false, "unexpected arguments"}, {[]string{"--version", "extra"}, false, "unexpected arguments"},
		{[]string{"admin", "typo"}, false, "unknown admin command"}, {[]string{"admin"}, false, "usage: gateway admin"},
		{[]string{"admin", "users", "--unknown"}, false, "flag provided but not defined"},
	} {
		t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIProcessChild$")
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"GATEWAY_CLI_TEST_CHILD=1", "GATEWAY_CLI_TEST_ARGS=" + strings.Join(tc.args, "\n"), "DATABASE_URL=postgres://synthetic:synthetic@" + ln.Addr().String() + "/isolated?sslmode=disable", "REDIS_URL=redis://" + ln.Addr().String(), "PORT=" + fmt.Sprint(ln.Addr().(*net.TCPAddr).Port), "CLIENT_API_KEY=synthetic-cli", "WEBHOOK_SIGNING_SECRET=synthetic-cli", "MEDIA_URL_SIGNING_SECRET=synthetic-cli"}
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
