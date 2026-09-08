package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type shellPanel struct {
	terminal      *interactiveTerminal
	interactive   bool
	open, focused bool
	running       bool
	testRun       bool
	testTarget    string
	stopping      bool
	input         []rune
	output        []string
	history       []string
	historyIndex  int
	scroll        int
	done          chan shellResult
	cmd           *exec.Cmd
}

type shellResult struct {
	output []byte
	err    error
}

func (s *shellPanel) handle(k key) {
	switch k.code {
	case keyEnter:
		command := strings.TrimSpace(string(s.input))
		if command != "" && !s.running {
			s.start(command)
			s.history = append(s.history, command)
			s.historyIndex = len(s.history)
		}
		s.input = nil
	case keyBackspace:
		if len(s.input) > 0 {
			s.input = s.input[:len(s.input)-1]
		}
	case keyUp:
		if s.historyIndex > 0 {
			s.historyIndex--
			s.input = []rune(s.history[s.historyIndex])
		}
	case keyDown:
		if s.historyIndex+1 < len(s.history) {
			s.historyIndex++
			s.input = []rune(s.history[s.historyIndex])
		} else {
			s.historyIndex = len(s.history)
			s.input = nil
		}
	default:
		if k.r == 3 && s.running && s.cmd != nil && s.cmd.Process != nil { // Ctrl+C
			s.stop()
		} else if k.r == 12 { // Ctrl+L
			s.output = nil
		} else if k.r >= 32 && k.r != 127 && !s.running {
			s.input = append(s.input, k.r)
		}
	}
	s.scroll = 0
}

func (s *shellPanel) start(command string) {
	s.testRun = false
	s.interactive = false
	s.output = append(s.output, "$ "+command)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-lc", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	s.running, s.stopping, s.done = true, false, make(chan shellResult, 1)
	if err := cmd.Start(); err != nil {
		s.running = false
		s.appendResult(nil, err)
		return
	}
	s.cmd = cmd
	done := s.done
	go func() {
		err := cmd.Wait()
		done <- shellResult{output.Bytes(), err}
	}()
}

func (s *shellPanel) stop() bool {
	if !s.running || s.cmd == nil || s.cmd.Process == nil {
		return false
	}
	signal := syscall.SIGINT
	if s.stopping {
		signal = syscall.SIGKILL
	}
	s.stopping = true
	if err := syscall.Kill(-s.cmd.Process.Pid, signal); err != nil {
		_ = s.cmd.Process.Kill()
	}
	return true
}

func (s *shellPanel) poll() bool {
	if !s.running {
		return false
	}
	select {
	case result := <-s.done:
		s.running, s.cmd = false, nil
		s.appendResult(result.output, result.err)
		s.stopping = false
		return true
	default:
		return false
	}
}

func (s *shellPanel) appendResult(out []byte, err error) {
	text := strings.TrimRight(string(out), "\r\n")
	if text != "" {
		s.output = append(s.output, strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")...)
	}
	if err != nil {
		s.output = append(s.output, "exit: "+err.Error())
	}
	if s.testRun {
		result := "Tests passed"
		if err != nil {
			result = "Tests failed"
		}
		if s.stopping {
			result = "Tests stopped"
		}
		s.output = append(s.output, result)
	}
	if len(s.output) > 500 {
		s.output = s.output[len(s.output)-500:]
	}
}

func (s *shellPanel) visibleLines(height int) []string {
	end := max(0, len(s.output)-s.scroll)
	start := max(0, end-height)
	return s.output[start:end]
}
