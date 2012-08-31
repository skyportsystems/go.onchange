// Command go.onchange automates the compile-restart-test cycle for
// developing go based applications by monitoring for changes in source
// files and dependencies.
//
// It will:
//     - Install packages.
//     - Restart application.
//     - Run relevant tests.
//     - Clear the screen between cyles to provide a clean log view.
//
// Installation:
//
//     go get github.com/daaku/go.onchange
//
// Usage:
//
//     go get github.com/daaku/rell
//     go.onchange github.com/daaku/rell
//
// TODO:
//     - Colors.
//     - Intelligent test execution.
//     - Intelligent screen clearing while working on tests.
package main

import (
	"flag"
	"fmt"
	"github.com/daaku/go.pkgwatcher"
	"github.com/daaku/go.tool"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
)

// The main Monitor instance.
type Monitor struct {
	IncludePattern   string
	RunTests         bool
	Verbose          bool
	ClearScreen      bool
	IncludePatternRe *regexp.Regexp
	Watcher          *pkgwatcher.Watcher
	ImportPath       string
	Args             []string
	eventLock        sync.Locker
	lastCommandError *tool.CommandError
	process          *os.Process
}

type restartResult int

const (
	restartNecessary restartResult = iota
	restartUnnecessary
	restartBuildFailed
)

// Printf for verbose mode.
func (m *Monitor) Printf(format string, v ...interface{}) {
	if m.Verbose {
		log.Printf(format, v...)
	}
}

// Clear the screen if configured to do so.
func (m *Monitor) Clear() {
	if m.ClearScreen {
		fmt.Printf("\033[2J")
		fmt.Printf("\033[H")
	}
}

// Checks and also updates the last command.
func (m *Monitor) isSameAsLastCommandError(err error) bool {
	commandError, ok := err.(*tool.CommandError)
	if !ok {
		return false
	}
	last := m.lastCommandError
	if last != nil && string(last.StdErr()) == string(commandError.StdErr()) {
		return true
	}
	m.lastCommandError = commandError
	return false
}

// Restart already installed binary.
func (m *Monitor) Restart() {
	m.Printf("restart requested")
	bin, err := exec.LookPath(filepath.Base(m.ImportPath))
	if err != nil {
		log.Printf("error finding binary: %s", err)
		return
	}
	m.Clear()
	if m.process != nil {
		m.process.Kill()
		m.process.Wait()
		m.process = nil
	}
	m.process, err = os.StartProcess(bin, m.Args, &os.ProcAttr{
		Files: []*os.File{
			nil,
			os.Stdout,
			os.Stderr,
		},
	})
	if err != nil {
		log.Printf("Failed to run command %s: %s", bin, err)
	}
}

// Install a library package.
func (m *Monitor) Install(importPath string) restartResult {
	options := tool.Options{
		ImportPaths: []string{importPath},
		Verbose:     true,
	}
	affected, err := options.Command("install")
	if err != nil {
		m.Printf("Install Error: %v", err)
	} else {
		m.Printf("Install Affected: %v", affected)
	}
	if err == nil && len(affected) == 0 {
		return restartUnnecessary
	}
	return restartNecessary
}

// Test a package.
func (m *Monitor) Test(importPath string) {
	options := tool.Options{
		ImportPaths: []string{importPath},
	}
	_, err := options.Command("test")
	if err != nil && !m.isSameAsLastCommandError(err) {
		log.Print(err)
	}
}

// Check if a file change should be ignored.
func (m *Monitor) ShouldIgnore(name string) bool {
	if filepath.Base(name)[0] == '.' {
		m.Printf("Ignored changed dot file %s", name)
		return true
	} else if m.IncludePatternRe.Match([]byte(name)) {
		return false
	}

	m.Printf("Ignored changed file %s", name)
	return true
}

// Watcher event handler.
func (m *Monitor) Event(ev *pkgwatcher.Event) {
	if m.ShouldIgnore(ev.Name) {
		return
	}

	go m.Watcher.WatchImportPath(ev.Package.ImportPath, true)
	m.eventLock.Lock()
	defer m.eventLock.Unlock()
	m.Printf("Change triggered restart: %+v", ev)
	var installR restartResult
	m.Printf("Installing all packages.")
	installR = m.Install("all")
	if installR == restartUnnecessary {
		m.Printf("Skipping because did not install anything.")
	} else {
		m.Restart()
	}
	if m.RunTests {
		m.Printf("Testing %s.", ev.Package.ImportPath)
		m.Test(ev.Package.ImportPath)
	}
}

// Start the main blocking Monitor loop.
func (m *Monitor) Start() {
	m.Restart()
	for {
		m.Printf("Main loop iteration.")
		select {
		case ev := <-m.Watcher.Event:
			go m.Event(ev)
		case err := <-m.Watcher.Error:
			log.Println("watcher error:", err)
		}
	}
}

func main() {
	monitor := &Monitor{
		eventLock: new(sync.Mutex),
	}
	flag.StringVar(&monitor.IncludePattern, "f", ".",
		"regexp pattern to match file names against to watch for changes")
	flag.BoolVar(&monitor.RunTests, "t", true, "run tests on change")
	flag.BoolVar(&monitor.Verbose, "v", false, "verbose")
	flag.BoolVar(&monitor.ClearScreen, "c", true, "clear screen on restart")
	flag.Parse()

	monitor.ImportPath = flag.Arg(0)
	monitor.Args = append(
		[]string{filepath.Base(monitor.ImportPath)},
		flag.Args()[1:flag.NArg()]...)

	re, err := regexp.Compile(monitor.IncludePattern)
	if err != nil {
		log.Fatal("Failed to compile given regexp pattern: %s", monitor.IncludePattern)
	}
	monitor.IncludePatternRe = re

	watcher, err := pkgwatcher.NewWatcher([]string{monitor.ImportPath}, "")
	if err != nil {
		log.Fatal(err)
	}
	monitor.Watcher = watcher

	monitor.Start()
}
