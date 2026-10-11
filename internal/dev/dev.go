// Package dev implements `weld dev`: it runs the backend and, when the web
// capability is installed, the frontend development server side by side under a
// small supervisor.
//
// The supervisor streams both processes' output with a colored name prefix,
// reacts to Ctrl-C, and reads a few control commands from stdin (restart,
// open, status, help, quit). Every setting comes from weld.yml with defaults
// derived from the installed capabilities, so `weld dev` needs no arguments in
// a project that `weld add web` produced.
package dev

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// Request describes a dev session.
type Request struct {
	// Dir is the project directory.
	Dir string
	// Config is the parsed weld.yml, or nil.
	Config *projectcfg.Config
	// FrontendInstalled reports whether the web capability is recorded.
	FrontendInstalled bool
	// Stdin carries control commands. A nil Stdin reads from os.Stdin.
	Stdin io.Reader
	// Stdout and Stderr receive the prefixed process output.
	Stdout io.Writer
	Stderr io.Writer
}

const (
	colorReset   = "\033[0m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"

	backendName  = "backend"
	frontendName = "frontend"
	devName      = "dev"

	scannerBuffer = 1024 * 1024
	stopTimeout   = 5 * time.Second
)

var (
	// localURLPattern matches the "Local: http://..." line Vite prints.
	localURLPattern = regexp.MustCompile(`(?:^|\s)Local:\s*(https?://[^\s"'<>]+)`)
	// ansiPattern strips ANSI color codes before URL matching.
	ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

// Run executes a dev session and returns when the user quits or a signal
// arrives. A process that exits unexpectedly is reported but does not end the
// session, so a frontend crash keeps the backend reachable.
func Run(req Request) error {
	stdin := req.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := req.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	specs, err := specs(req)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	supervisor := newSupervisor(ctx, specs, stdin, stdout, stderr)
	return supervisor.run()
}

// specs derives the process list from weld.yml and the installed capabilities.
func specs(req Request) ([]spec, error) {
	cfg := req.Config.EffectiveDev()
	dir := req.Dir

	backendCmd := cfg.Backend.Cmd
	var backendArgs []string
	if backendCmd == "" {
		backendCmd = "go"
		backendArgs = append([]string{"run", "."}, cfg.Backend.Args...)
	} else {
		backendArgs = splitCommand(backendCmd)
		if len(backendArgs) == 0 {
			return nil, errors.New("weld.yml: dev.backend.cmd is empty")
		}
		backendCmd = backendArgs[0]
		backendArgs = backendArgs[1:]
	}

	list := []spec{{
		Name:  backendName,
		Color: colorBlue,
		Path:  backendCmd,
		Args:  backendArgs,
		Dir:   dir,
	}}

	if !req.FrontendInstalled {
		return list, nil
	}

	frontendArgs := splitCommand(cfg.Frontend.Cmd)
	if len(frontendArgs) == 0 {
		return nil, errors.New("weld.yml: dev.frontend.cmd is empty")
	}
	pm := cfg.Frontend.Pm
	if pm == "" {
		pm = "pnpm"
	}
	frontendDir := filepath.Join(dir, cfg.Frontend.Dir)
	if info, statErr := os.Stat(frontendDir); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("frontend directory not found: %s", frontendDir)
	}
	list = append(list, spec{
		Name:  frontendName,
		Color: colorMagenta,
		Path:  pm,
		Args:  frontendArgs,
		Dir:   frontendDir,
		URL:   inferFrontendURL(frontendArgs),
	})
	return list, nil
}

// spec is one supervised process.
type spec struct {
	Name  string
	Color string
	Path  string
	Args  []string
	Dir   string
	URL   string
}

// managed is a running spec.
type managed struct {
	spec       spec
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	done       chan struct{}
	stopping   bool
	restarting bool
}

type exitEvent struct {
	name string
	cmd  *exec.Cmd
	err  error
}

// supervisor owns the running processes and the control loop.
type supervisor struct {
	ctx      context.Context
	cancel   context.CancelFunc
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	outputMu sync.Mutex
	stateMu  sync.RWMutex

	order     []string
	processes map[string]*managed
	exits     chan exitEvent
	commands  chan string

	shuttingDown bool
	frontendURL  string
}

func newSupervisor(ctx context.Context, specs []spec, stdin io.Reader, stdout, stderr io.Writer) *supervisor {
	ctx, cancel := context.WithCancel(ctx)
	processes := make(map[string]*managed, len(specs))
	order := make([]string, 0, len(specs))
	for _, s := range specs {
		processes[s.Name] = &managed{spec: s}
		order = append(order, s.Name)
	}
	s := &supervisor{
		ctx:       ctx,
		cancel:    cancel,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		order:     order,
		processes: processes,
		exits:     make(chan exitEvent, len(specs)*4),
		commands:  make(chan string, 8),
	}
	if frontend, ok := processes[frontendName]; ok {
		s.frontendURL = frontend.spec.URL
	}
	return s
}

func (s *supervisor) run() error {
	for _, name := range s.order {
		if err := s.start(name); err != nil {
			s.message(fmt.Sprintf("failed to start %s: %v", name, err))
		}
	}
	s.message("commands: rs backend|b | rs frontend|f | rs all|a | o | status|st | help|h | quit")
	go s.readCommands()

	for {
		select {
		case <-s.ctx.Done():
			s.shutdown()
			if errors.Is(s.ctx.Err(), context.Canceled) {
				return nil
			}
			return s.ctx.Err()
		case line, ok := <-s.commands:
			if !ok {
				s.commands = nil
				continue
			}
			s.handle(line)
		case event := <-s.exits:
			s.handleExit(event)
			if s.shuttingDown {
				return nil
			}
		}
	}
}

func (s *supervisor) start(name string) error {
	proc, ok := s.processes[name]
	if !ok {
		return fmt.Errorf("unknown process %q", name)
	}
	cmd := exec.CommandContext(s.ctx, proc.spec.Path, proc.spec.Args...)
	cmd.Dir = proc.spec.Dir
	configureProcess(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("prepare stdin for %s: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare stdout for %s: %w", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("prepare stderr for %s: %w", name, err)
	}

	go s.stream(stdout, s.stdout, proc.spec.Name, proc.spec.Color)
	go s.stream(stderr, s.stderr, proc.spec.Name, proc.spec.Color)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	proc.cmd = cmd
	proc.stdin = stdin
	proc.done = make(chan struct{})
	proc.stopping = false
	proc.restarting = false

	done := proc.done
	go func() {
		err := cmd.Wait()
		s.exits <- exitEvent{name: name, cmd: cmd, err: err}
		close(done)
	}()
	s.message(fmt.Sprintf("started %s", name))
	return nil
}

func (s *supervisor) stop(name string, restart bool) error {
	proc, ok := s.processes[name]
	if !ok {
		return fmt.Errorf("unknown process %q", name)
	}
	if proc.cmd == nil {
		proc.restarting = restart
		return nil
	}
	proc.stopping = true
	proc.restarting = restart
	if proc.stdin != nil {
		_ = proc.stdin.Close()
	}
	if err := interruptProcess(proc.cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt %s: %w", name, err)
	}
	select {
	case <-proc.done:
		return nil
	case <-time.After(stopTimeout):
		s.message(fmt.Sprintf("%s did not exit in time, killing it", name))
		if err := killProcess(proc.cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill %s: %w", name, err)
		}
		<-proc.done
		return nil
	}
}

func (s *supervisor) restart(target string) error {
	names, err := expandTarget(target, s.order)
	if err != nil {
		return err
	}
	for _, name := range names {
		if s.running(name) {
			s.message(fmt.Sprintf("restarting %s", name))
			if err := s.stop(name, true); err != nil {
				s.message(fmt.Sprintf("failed to restart %s: %v", name, err))
			}
			continue
		}
		s.message(fmt.Sprintf("starting %s", name))
		if err := s.start(name); err != nil {
			s.message(fmt.Sprintf("failed to start %s: %v", name, err))
		}
	}
	return nil
}

func (s *supervisor) shutdown() {
	if s.shuttingDown {
		return
	}
	s.shuttingDown = true
	for _, name := range s.order {
		_ = s.stop(name, false)
	}
}

func (s *supervisor) handleExit(event exitEvent) {
	proc, ok := s.processes[event.name]
	if !ok || proc.cmd != event.cmd {
		return
	}
	plannedStop := proc.stopping
	shouldRestart := proc.restarting
	proc.cmd = nil
	proc.stdin = nil
	proc.done = nil
	proc.stopping = false
	proc.restarting = false
	if event.name == frontendName {
		s.setFrontendURL("")
	}
	if s.shuttingDown {
		return
	}
	if shouldRestart {
		if err := s.start(event.name); err != nil {
			s.message(fmt.Sprintf("failed to restart %s: %v", event.name, err))
		}
		return
	}
	if plannedStop {
		return
	}
	if event.err != nil {
		s.message(fmt.Sprintf("%s exited unexpectedly: %v", event.name, event.err))
		return
	}
	s.message(fmt.Sprintf("%s exited unexpectedly", event.name))
}

func (s *supervisor) handle(line string) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "rs", "restart":
		if len(fields) != 2 {
			s.message("usage: rs backend|b | frontend|f | all|a")
			return
		}
		if err := s.restart(fields[1]); err != nil {
			s.message(err.Error())
		}
	case "o", "open":
		s.openFrontend()
	case "status", "st":
		s.printStatus()
	case "help", "h":
		s.message("commands: rs backend|b | rs frontend|f | rs all|a | o | status|st | help|h | quit")
	case "q", "quit", "exit":
		s.cancel()
	default:
		s.message("unknown command, use: rs backend|b | rs frontend|f | rs all|a | o | status|st | help|h | quit")
	}
}

func (s *supervisor) printStatus() {
	for _, name := range s.order {
		state := "stopped"
		if s.running(name) {
			state = "running"
		}
		s.message(fmt.Sprintf("%s: %s", name, state))
	}
}

func (s *supervisor) openFrontend() {
	proc, ok := s.processes[frontendName]
	if !ok || proc.cmd == nil {
		s.message("frontend is not running")
		return
	}
	target := s.getFrontendURL()
	if target == "" {
		s.message("frontend url not detected yet")
		return
	}
	if err := openBrowser(target); err != nil {
		s.message(fmt.Sprintf("open %s: %v", target, err))
		return
	}
	s.message(fmt.Sprintf("opened %s", target))
}

func (s *supervisor) stream(src io.Reader, dst io.Writer, name, color string) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerBuffer)
	for scanner.Scan() {
		line := scanner.Text()
		if name == frontendName {
			if detected, ok := detectFrontendURL(line); ok {
				s.setFrontendURL(detected)
			}
		}
		s.outputMu.Lock()
		// fmt.Fprintln(dst, fmt.Sprintf("%s[%s]%s %s", color, name, colorReset, line))
		fmt.Fprintf(dst, "%s[%s]%s %s\n", color, name, colorReset, line)
		s.outputMu.Unlock()
	}
	_ = scanner.Err() // ignore errors, stdin may be closed
}

func (s *supervisor) readCommands() {
	scanner := bufio.NewScanner(s.stdin)
	for scanner.Scan() {
		select {
		case <-s.ctx.Done():
			return
		case s.commands <- scanner.Text():
		}
	}
	_ = scanner.Err() // ignore errors, stdin may be closed
	close(s.commands)
}

func (s *supervisor) message(line string) {
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	fmt.Fprintf(s.stdout, "%s[%s]%s %s\n", colorReset, devName, colorReset, line)
}

func (s *supervisor) running(name string) bool {
	proc, ok := s.processes[name]
	return ok && proc.cmd != nil
}

func (s *supervisor) setFrontendURL(value string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.frontendURL = value
}

func (s *supervisor) getFrontendURL() string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.frontendURL
}

// expandTarget maps a control-command target to process names.
func expandTarget(target string, order []string) ([]string, error) {
	switch target {
	case backendName, "b":
		return []string{backendName}, nil
	case frontendName, "f":
		return []string{frontendName}, nil
	case "all", "a":
		return order, nil
	default:
		return nil, fmt.Errorf("unknown process %q", target)
	}
}

// detectFrontendURL extracts the "Local:" URL from a dev server line.
func detectFrontendURL(line string) (string, bool) {
	line = ansiPattern.ReplaceAllString(line, "")
	match := localURLPattern.FindStringSubmatch(line)
	if len(match) < 2 {
		return "", false
	}
	parsed, err := url.Parse(match[1])
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return parsed.String(), true
}

// inferFrontendURL guesses the dev server URL from the command's --host/--port
// arguments, matching Vite's defaults.
func inferFrontendURL(args []string) string {
	host := "localhost"
	port := 5173
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--host" && i+1 < len(args):
			host = normalizeHost(args[i+1])
			i++
		case strings.HasPrefix(arg, "--host="):
			host = normalizeHost(strings.TrimPrefix(arg, "--host="))
		case arg == "--port" && i+1 < len(args):
			if p, err := strconv.Atoi(args[i+1]); err == nil && p > 0 {
				port = p
			}
			i++
		case strings.HasPrefix(arg, "--port="):
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil && p > 0 {
				port = p
			}
		}
	}
	return fmt.Sprintf("http://%s:%d/", host, port)
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return "localhost"
	}
	return host
}

// splitCommand splits a configured command on whitespace. weld.yml commands are
// simple argument lists, not shell programs, so no quoting or expansion applies.
func splitCommand(command string) []string {
	return strings.Fields(command)
}
