package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const maxProcessAncestors = 32

type processAncestor struct {
	pid           int
	parentPID     int
	name          string
	terminalProxy bool
}

func processAncestry(pid int) []processAncestor {
	ancestors := make([]processAncestor, 0, 8)
	seen := make(map[int]struct{})
	for len(ancestors) < maxProcessAncestors && pid > 0 {
		if _, duplicate := seen[pid]; duplicate {
			break
		}
		seen[pid] = struct{}{}

		ancestor, err := readProcessAncestor(pid)
		if err != nil {
			break
		}
		ancestors = append(ancestors, ancestor)
		if ancestor.parentPID == pid {
			break
		}
		pid = ancestor.parentPID
	}
	return ancestors
}

func readProcessAncestor(pid int) (processAncestor, error) {
	output, err := exec.Command(
		"ps",
		"-o", "ppid=",
		"-o", "args=",
		"-p", strconv.Itoa(pid),
	).Output()
	if err != nil {
		return processAncestor{}, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	return parseProcessAncestor(pid, string(output))
}

func parseProcessAncestor(pid int, row string) (processAncestor, error) {
	row = strings.TrimSpace(row)
	fields := strings.Fields(row)
	if len(fields) < 2 {
		return processAncestor{}, fmt.Errorf("parse process %d: malformed ps output", pid)
	}
	parentPID, err := strconv.Atoi(fields[0])
	if err != nil {
		return processAncestor{}, fmt.Errorf("parse process %d parent: %w", pid, err)
	}

	args := strings.TrimSpace(strings.TrimPrefix(row, fields[0]))
	name, proxy := processName(args)
	return processAncestor{
		pid:           pid,
		parentPID:     parentPID,
		name:          name,
		terminalProxy: proxy,
	}, nil
}

func processName(args string) (string, bool) {
	fields := strings.Fields(args)
	for index, field := range fields {
		if index > 1 {
			break
		}
		candidate := strings.Trim(field, `"'()`)
		if strings.EqualFold(filepath.Base(candidate), "kiro-cli-term") {
			return "kiro-cli-term", true
		}
	}
	if len(fields) == 0 {
		return "unknown", false
	}
	return filepath.Base(strings.Trim(fields[0], `"'`)), false
}

func formatProcessAncestry(ancestors []processAncestor) string {
	parts := make([]string, 0, len(ancestors))
	for _, ancestor := range ancestors {
		parts = append(parts, fmt.Sprintf("%d:%s", ancestor.pid, ancestor.name))
	}
	return strings.Join(parts, " <- ")
}

func terminalProxyAncestor(ancestors []processAncestor) (processAncestor, bool) {
	for _, ancestor := range ancestors {
		if ancestor.terminalProxy {
			return ancestor, true
		}
	}
	return processAncestor{}, false
}
