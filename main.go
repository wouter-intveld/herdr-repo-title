// Command herdr-repo-title names each Herdr tab after the git repository its
// focused pane is in.
//
// Herdr emits no event when a shell changes directory, so the plugin polls
// session.snapshot once a second. `run` is the poll loop, started by the
// plugin's startup hook; `restart` replaces a running loop with a fresh one,
// since Herdr runs startup hooks only when its server starts.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	pollInterval = time.Second
	callTimeout  = 2 * time.Second
	// Consecutive failed polls after which the loop gives up.
	maxFailures = 30
	// How long a new loop waits for the one it replaces to let go of the lock.
	lockWait = 5 * time.Second
)

func main() {
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "run":
		err = run()
	case "restart":
		err = restart()
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [run|restart]\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "herdr-repo-title:", err)
		os.Exit(1)
	}
}

// paths keeps per-session files apart: the plugin state dir is shared by every
// Herdr session, the socket path is not.
type paths struct {
	socket, lock, state, log string
}

func resolvePaths() (paths, error) {
	socket := os.Getenv("HERDR_SOCKET_PATH")
	if socket == "" {
		return paths{}, errors.New("HERDR_SOCKET_PATH is not set; run me from Herdr")
	}
	dir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "herdr-repo-title")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return paths{}, err
	}
	sum := sha256.Sum256([]byte(socket))
	base := filepath.Join(dir, hex.EncodeToString(sum[:6]))
	return paths{socket: socket, lock: base + ".lock", state: base + ".json", log: base + ".log"}, nil
}

func run() error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	// Lock before touching the log: a losing instance must not truncate the
	// running one's log. Its error goes to stderr, which Herdr records.
	lock, err := acquireLock(p.lock, lockWait)
	if err != nil {
		return err
	}
	defer lock.Close()

	logFile, err := os.OpenFile(p.log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	origin, err := os.Stat(p.socket)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()

	c := client{socket: p.socket, timeout: callTimeout}
	written := loadState(p.state)
	log.Printf("started, pid %d, socket %s", os.Getpid(), p.socket)

	failures := 0
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		// A new server binds a new socket file: this loop belongs to the old one.
		if fi, err := os.Stat(p.socket); err != nil || !os.SameFile(origin, fi) {
			log.Print("herdr server gone, exiting")
			return nil
		}

		snap, err := c.snapshot()
		if err != nil {
			failures++
			if failures == 1 {
				log.Print(err)
			}
			if failures >= maxFailures {
				return fmt.Errorf("giving up after %d failed polls: %w", failures, err)
			}
		} else {
			failures = 0
			if apply(c, snap, written) {
				if err := saveState(p.state, written); err != nil {
					log.Print(err)
				}
			}
		}

		select {
		case <-ctx.Done():
			log.Print("stopped")
			return nil
		case <-ticker.C:
		}
	}
}

// apply renames every managed tab whose title is out of date and records what
// it wrote. It reports whether the record changed.
func apply(c client, snap snapshot, written map[string]string) bool {
	focused := make(map[string]string, len(snap.Layouts))
	for _, l := range snap.Layouts {
		focused[l.TabID] = l.FocusedPaneID
	}
	byID := make(map[string]pane, len(snap.Panes))
	first := make(map[string]pane, len(snap.Tabs))
	for _, p := range snap.Panes {
		byID[p.ID] = p
		if _, ok := first[p.TabID]; !ok {
			first[p.TabID] = p
		}
	}

	changed := false
	live := make(map[string]bool, len(snap.Tabs))
	for _, t := range snap.Tabs {
		live[t.ID] = true
		if !managed(t.Label, written[t.ID]) {
			continue
		}
		p, ok := byID[focused[t.ID]]
		if !ok {
			p, ok = first[t.ID]
		}
		if !ok {
			continue
		}
		dir := paneDir(p)
		if dir == "" {
			continue
		}
		want := title(dir)
		if t.Label != want {
			if err := c.renameTab(t.ID, want); err != nil {
				log.Print(err)
				continue
			}
			log.Printf("%s: %q -> %q", t.ID, t.Label, want)
		}
		if written[t.ID] != want {
			written[t.ID] = want
			changed = true
		}
	}
	for id := range written {
		if !live[id] {
			delete(written, id)
			changed = true
		}
	}
	return changed
}

// The state file maps tab id to the label this plugin last wrote, so a tab
// named by an earlier run is still recognised as ours after a restart.
func loadState(path string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			log.Printf("ignoring corrupt state %s: %v", path, err)
			m = map[string]string{}
		}
	}
	return m
}

func saveState(path string, m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// acquireLock takes an exclusive flock on path, retrying until wait elapses,
// and records the holder's pid in it. The kernel drops the lock when the
// process dies, so a crash never leaves a stale lock behind.
func acquireLock(path string, wait time.Duration) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("another instance holds %s: %w", path, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// restart stops the loop holding the lock, if any, and starts a detached one
// in its place. The new loop must not inherit this action's stdout/stderr:
// Herdr reads those to EOF and would count the action as running forever.
func restart() error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if pid, ok := lockHolder(p.lock); ok {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stopping pid %d: %w", pid, err)
		}
		fmt.Printf("stopped pid %d\n", pid)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, "run")
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("started pid %d, log %s\n", cmd.Process.Pid, p.log)
	return cmd.Process.Release()
}

// lockHolder returns the pid recorded in the lock file, but only while the
// lock is actually held: a pid from a dead instance may have been reused.
func lockHolder(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}
