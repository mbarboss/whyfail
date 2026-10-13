package apply

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// WarningsFor returns a warning function like Warnings that also flags fixes
// installing a package whose name appears in output, the captured output the
// model saw. Such a name may be a typo or injected text, and anyone can
// publish a package under it (typosquatting).
func WarningsFor(output string) func(command string) []string {
	if output == "" {
		return Warnings
	}
	lower := strings.ToLower(output)
	out := searchable{lower: lower, python: pythonNorm.Replace(lower)}
	return func(command string) []string {
		found := Warnings(command)
		if out.installsNamedPackage(command) {
			found = append(found, NamedPackage)
		}
		return found
	}
}

// searchable is the captured output prepared once for name lookups.
type searchable struct {
	lower, python string
}

// pythonNorm compares names like the Python package index does: "-", "_"
// and "." are the same.
var pythonNorm = strings.NewReplacer("_", "-", ".", "-")

type ecosystem int

const (
	python ecosystem = iota + 1
	javascript
	ruby
	rust
	golang
)

// segmentSep splits a command line into simple commands.
var segmentSep = regexp.MustCompile(`&&|\|\||[;|&\n]`)

// valueFlags take the next argument as their value, so it is not a package.
// They are per ecosystem because the same letter differs: "-v" is a version
// for gem but "verbose" for pip and cargo, "-f" is find-links for pip but
// "force" for npm.
var valueFlags = map[ecosystem][]string{
	python: {
		"-r", "--requirement", "-c", "--constraint", "-e", "--editable", "-i", "--index-url",
		"--extra-index-url", "-f", "--find-links", "-t", "--target", "--prefix", "--root",
	},
	javascript: {"--registry", "-w", "--workspace", "--prefix"},
	ruby:       {"-v", "--version", "-s", "--source", "-i", "--install-dir"},
	rust:       {"--git", "--path", "--version", "--registry", "--root", "-F", "--features", "--branch", "--tag", "--rev"},
}

func (s searchable) installsNamedPackage(command string) bool {
	for _, segment := range segmentSep.Split(command, -1) {
		eco, args := installArgs(strings.Fields(segment))
		for _, name := range packageNames(eco, args) {
			if eco == python {
				if containsWord(s.python, pythonNorm.Replace(name)) {
					return true
				}
			} else if containsWord(s.lower, name) {
				return true
			}
		}
	}
	return false
}

// installArgs recognizes a package install in fields and returns its
// ecosystem and the arguments after the install subcommand.
func installArgs(fields []string) (ecosystem, []string) {
	for len(fields) > 0 && (fields[0] == "sudo" || fields[0] == "doas" || isAssignment(fields[0])) {
		fields = fields[1:]
	}
	if len(fields) < 2 {
		return 0, nil
	}
	tool := program(fields[0])
	sub, rest := strings.ToLower(fields[1]), fields[2:]
	switch {
	case (tool == "pip" || tool == "pip3" || tool == "pipx") && sub == "install":
		return python, rest
	case (tool == "python" || tool == "python3" || tool == "py") && sub == "-m" &&
		len(rest) >= 2 && (program(rest[0]) == "pip" || program(rest[0]) == "pip3") && strings.EqualFold(rest[1], "install"):
		return python, rest[2:]
	case tool == "uv" && sub == "pip" && len(rest) >= 1 && strings.EqualFold(rest[0], "install"):
		return python, rest[1:]
	case tool == "uv" && sub == "add":
		return python, rest
	case tool == "npm" && (sub == "install" || sub == "i" || sub == "add"),
		tool == "pnpm" && (sub == "add" || sub == "install" || sub == "i"),
		tool == "yarn" && sub == "add":
		return javascript, rest
	case tool == "gem" && sub == "install":
		return ruby, rest
	case tool == "cargo" && (sub == "install" || sub == "add"):
		return rust, rest
	case tool == "go" && (sub == "install" || sub == "get"):
		return golang, rest
	}
	return 0, nil
}

// program returns the lower-case base name of an executable, without .exe.
func program(s string) string {
	s = strings.ToLower(strings.Trim(s, `"'`))
	s = path.Base(strings.ReplaceAll(s, `\`, "/"))
	return strings.TrimSuffix(s, ".exe")
}

func isAssignment(s string) bool {
	i := strings.IndexByte(s, '=')
	return i > 0 && !strings.ContainsAny(s[:i], `-/\"'`)
}

// packageNames returns the package names in args, without flags, flag
// values, local paths, URLs, versions or extras.
func packageNames(eco ecosystem, args []string) []string {
	var names []string
	for i := 0; i < len(args); i++ {
		arg := strings.Trim(args[i], `"'`)
		switch {
		case slices.Contains(valueFlags[eco], arg):
			i++
			continue
		case strings.HasPrefix(arg, "-"),
			strings.HasPrefix(arg, "."), strings.HasPrefix(arg, "/"), strings.HasPrefix(arg, "~"),
			strings.Contains(arg, `\`), strings.Contains(arg, "://"):
			continue
		}
		if name := stripVersion(eco, arg); name != "" {
			names = append(names, strings.ToLower(name))
		}
	}
	return names
}

func stripVersion(eco ecosystem, arg string) string {
	switch eco {
	case python:
		if i := strings.IndexAny(arg, "=<>!~;[@ "); i >= 0 {
			arg = arg[:i]
		}
	case javascript:
		// "@scope/name@1.2" keeps its leading "@"; a version follows the next one.
		scope, body := strings.HasPrefix(arg, "@"), strings.TrimPrefix(arg, "@")
		if i := strings.IndexByte(body, '@'); i >= 0 {
			body = body[:i]
		}
		if !scope {
			return body
		}
		if i := strings.IndexByte(body, '/'); i <= 0 || i == len(body)-1 {
			return ""
		}
		return "@" + body
	case ruby, rust, golang:
		if i := strings.IndexByte(arg, '@'); i >= 0 {
			arg = arg[:i]
		}
	}
	return arg
}

// containsWord reports whether word appears in text with no name character
// right before or after it. Both must already be lower case.
func containsWord(text, word string) bool {
	for start := 0; start <= len(text); {
		i := strings.Index(text[start:], word)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(word)
		if (i == 0 || !isNameByte(text[i-1])) && (end == len(text) || !isNameByte(text[end])) {
			return true
		}
		start = i + 1
	}
	return false
}

func isNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}
