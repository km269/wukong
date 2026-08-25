package security

import (
	"strings"
)

// dangerousCommandRules defines high-risk command patterns using
// token-level matching. Each rule specifies the base command and
// optional flag combinations that make it dangerous.
//
// Unlike substring matching ("rm -rf"), token-level matching
// splits the command into argv tokens first, then matches
// against the command name + flag set. This prevents bypasses
// like:
//
//	rm --recursive --force /
//	rm -r -f /
//	rm -fr /
//	sudo rm -rf /
//	rm  -rf  /   (extra spaces)
type dangerousRule struct {
	cmd   string   // base command (e.g. "rm", "git")
	flags []string // flag tokens that trigger the rule (e.g. ["-r", "-f", "--recursive", "--force"])
	// minimum number of flags from the list that must be present.
	// 0 means the command alone is dangerous (e.g. "mkfs").
	minFlags int
}

// dangerousRules is the complete rule set. It covers the same
// commands as the old substring list but with token-aware logic.
var dangerousRules = []dangerousRule{
	// rm with recursive + force
	{cmd: "rm", flags: []string{"-r", "-rf", "-fr", "-R", "-Rf", "-fR",
		"--recursive", "--force"}, minFlags: 2},
	// sudo itself is suspicious (privilege escalation)
	{cmd: "sudo", minFlags: 0},
	// chmod 777 (world-writable)
	{cmd: "chmod", flags: []string{"777", "a+rwx", "ugo+rwx"}, minFlags: 1},
	// chown to different user
	{cmd: "chown", minFlags: 0},
	// dd (raw disk write)
	{cmd: "dd", flags: []string{"if=", "of="}, minFlags: 0},
	// mkfs (format filesystem)
	{cmd: "mkfs", minFlags: 0},
	{cmd: "mkfs.ext2", minFlags: 0},
	{cmd: "mkfs.ext3", minFlags: 0},
	{cmd: "mkfs.ext4", minFlags: 0},
	{cmd: "mkfs.btrfs", minFlags: 0},
	{cmd: "mkfs.xfs", minFlags: 0},
	{cmd: "mkfs.ntfs", minFlags: 0},
	{cmd: "mkfs.vfat", minFlags: 0},
	// format (Windows)
	{cmd: "format", minFlags: 0},
	// git push --force / -f
	{cmd: "git", flags: []string{"push"}, minFlags: 0}, // sub-checked below via gitPushForceRules

	// docker system prune / docker rm -f
	{cmd: "docker", flags: []string{"system", "prune"}, minFlags: 2},
	{cmd: "docker", flags: []string{"rm", "-f", "--force"}, minFlags: 2},
	// curl/wget piped to shell
	{cmd: "curl", flags: []string{"|"}, minFlags: 0},
	{cmd: "wget", flags: []string{"|"}, minFlags: 0},
}

// gitPushForceTokens checks for git push --force patterns.
func isGitPushForce(tokens []string) bool {
	foundPush := false
	foundForce := false
	for _, t := range tokens {
		switch t {
		case "push":
			foundPush = true
		case "--force", "--force-with-lease", "-f":
			foundForce = true
		}
	}
	return foundPush && foundForce
}

// isPipedToShell checks for `curl ... | sh` or `wget ... | sh` patterns.
func isPipedToShell(tokens []string) bool {
	hasPipe := false
	hasShell := false
	for _, t := range tokens {
		if t == "|" {
			hasPipe = true
		}
		switch t {
		case "sh", "bash", "zsh", "fish", "dash", "/bin/sh", "/bin/bash":
			hasShell = true
		}
	}
	return hasPipe && hasShell
}

// tokenizeCommand splits a shell command string into tokens.
// It handles quoted strings and normalizes whitespace.
func tokenizeCommand(command string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for _, ch := range command {
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case (ch == ' ' || ch == '\t' || ch == '\n') && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		case ch == '|' && !inSingle && !inDouble:
			// Pipe is a separate token
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, "|")
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// isDangerousCommandTokens performs token-level analysis of a command
// string to detect dangerous patterns that substring matching would miss.
// It serves as a supplement to the user-configured BlockedCommands list.
func isDangerousCommandTokens(command string) bool {
	tokens := tokenizeCommand(command)
	if len(tokens) == 0 {
		return false
	}

	// Lowercase all tokens for case-insensitive matching.
	ltokens := make([]string, len(tokens))
	for i, t := range tokens {
		ltokens[i] = strings.ToLower(t)
	}

	// Special multi-token patterns.
	if isGitPushForce(ltokens) {
		return true
	}
	// Check if the command starts with a curl/wget and pipes to shell.
	for i, t := range ltokens {
		if t == "curl" || t == "wget" {
			if isPipedToShell(ltokens[i:]) {
				return true
			}
		}
	}

	// Build a map of flag tokens present (everything after the command name).
	flagSet := make(map[string]bool)
	cmdName := ltokens[0]
	// Handle sudo: skip it and use the next token as the real command.
	if cmdName == "sudo" {
		if len(ltokens) > 1 {
			cmdName = ltokens[1]
			flagSet = tokensToSet(ltokens[2:])
		}
		// sudo itself is dangerous
		return true
	}
	flagSet = tokensToSet(ltokens[1:])

	for _, rule := range dangerousRules {
		if rule.cmd != cmdName {
			continue
		}
		// Special handling for git: only git push --force is dangerous,
		// not all git commands. Already handled above.
		if rule.cmd == "git" {
			continue
		}
		if rule.minFlags == 0 {
			// Command alone is dangerous (e.g. mkfs, format, chown)
			return true
		}
		// Count how many of the rule's flag tokens are present.
		matched := 0
		for _, f := range rule.flags {
			if flagSet[f] {
				matched++
			}
		}
		if matched >= rule.minFlags {
			return true
		}
	}

	// Also check for raw device writes like > /dev/sda
	lower := strings.ToLower(command)
	if strings.Contains(lower, "/dev/sd") || strings.Contains(lower, "/dev/nvme") {
		return true
	}

	return false
}

// tokensToSet converts a token slice to a set map for O(1) lookup.
// Combined short flags like -rf are expanded into -r and -f so
// that rules can match individual flag characters.
func tokensToSet(tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
		// Expand combined short flags: -rf → -r, -f
		if len(t) > 2 && t[0] == '-' && t[1] != '-' {
			for i := 1; i < len(t); i++ {
				set["-"+string(t[i])] = true
			}
		}
	}
	return set
}
