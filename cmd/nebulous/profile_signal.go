package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"
)

// installProfileDumpOnSignal makes SIGUSR1 write a heap and a goroutine
// profile into <user cache dir>/nebulous/pprof/, so a long-lived process
// (traversal-serve under cutting-garden) can be profiled on a live host with
// no restart and no config: `kill -USR1 <pid>`, then read the logged paths
// and inspect with `go tool pprof`. nebulous#58.
func installProfileDumpOnSignal(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				dir, err := writeProfiles()
				if err != nil {
					log.Printf("profile dump: %v", err)
					continue
				}
				log.Printf("profile dump: wrote heap and goroutine profiles to %s", dir)
			}
		}
	}()
}

// writeProfiles writes one timestamped heap + goroutine profile pair and
// returns the directory. A GC runs first so the heap profile reflects live
// objects rather than garbage awaiting collection.
func writeProfiles() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving cache dir: %w", err)
	}
	dir := filepath.Join(cache, "nebulous", "pprof")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	runtime.GC()
	for _, name := range []string{"heap", "goroutine"} {
		path := filepath.Join(dir, fmt.Sprintf("%s-%d-%s.pb.gz", name, os.Getpid(), stamp))
		f, err := os.Create(path)
		if err != nil {
			return "", err
		}
		werr := pprof.Lookup(name).WriteTo(f, 0)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return "", fmt.Errorf("writing %s: %w", path, werr)
		}
	}
	return dir, nil
}
