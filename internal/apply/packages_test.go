package apply

import (
	"slices"
	"strings"
	"testing"
)

func TestWarningsForFlagsPackageNamedInOutput(t *testing.T) {
	tests := []struct {
		command, output string
	}{
		{"pip install modulo_inexistente", "ModuleNotFoundError: No module named 'modulo_inexistente'"},
		{"pip3 install --user Modulo-Inexistente", "No module named 'modulo_inexistente'"},
		{"python3 -m pip install -U foo==1.2", "No module named 'foo'"},
		{"py -m pip install foo", "No module named 'foo'"},
		{"pip.exe install foo", "No module named 'foo'"},
		{"pipx install foo-cli", "foo-cli: command not found"},
		{"uv pip install foo", "No module named 'foo'"},
		{"uv add 'foo[extra]>=2'", "No module named 'foo'"},
		{"pip install -r requirements.txt foo", "No module named 'foo'"},
		{"npm install lodsh", "Error: Cannot find module 'lodsh'"},
		{"npm i -D @acme/utils@2", "Cannot find module '@acme/utils'"},
		{"pnpm add left-pad", "Cannot find module 'left-pad'"},
		{"yarn add left-pad", "Cannot find module 'left-pad'"},
		{"gem install rakee", "cannot load such file -- rakee"},
		{"cargo install ripgrp", "ripgrp: command not found"},
		{"cargo add serde@1", "unresolved import `serde`"},
		{"go get github.com/foo/bar@latest", "no required module provides package github.com/foo/bar"},
		{"go install github.com/foo/bar@v1.0.0", "no required module provides package github.com/foo/bar"},
		{"sudo pip install foo", "No module named 'foo'"},
		{"cd app && npm install lodsh", "Cannot find module 'lodsh'"},
		{"PIP_NO_CACHE_DIR=1 pip install foo", "No module named 'foo'"},
		{"pip install -v foo", "No module named 'foo'"},
		{"npm install -f lodsh", "Cannot find module 'lodsh'"},
		{"npm install -s lodsh", "Cannot find module 'lodsh'"},
		{"cargo install -v ripgrp", "ripgrp: command not found"},
		{"gem install -v 13.0 rakee", "cannot load such file -- rakee"},
		{"python -m pip INSTALL foo", "No module named 'foo'"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := WarningsFor(tt.output)(tt.command)

			if !slices.Contains(got, NamedPackage) {
				t.Errorf("warnings = %v, want them to contain %q", got, NamedPackage)
			}
		})
	}
}

func TestWarningsForIgnoresPackagesNotNamedInOutput(t *testing.T) {
	tests := []struct {
		command, output string
	}{
		{"pip install requests", "No module named 'yaml'"},
		{"pip install pyyaml", "No module named 'yaml'"},
		{"pip install -r requirements.txt", "ERROR: requirements.txt not found"},
		{"pip install -e .", "No module named 'app'"},
		{"pip install --index-url https://pypi.example.test/simple foo", "pypi.example.test unreachable"},
		{"npm install", "Cannot find module 'lodsh'"},
		{"npm install ./local-pkg", "Cannot find module 'local-pkg'"},
		{"npm install lodash", "Cannot find module 'lodash-es'"},
		{"npm run build", "Cannot find module 'build'"},
		{"go build ./cmd/foo", "foo: undefined"},
		{"echo pip install foo", "No module named 'foo'"},
		{"brew install foo", "foo: command not found"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := WarningsFor(tt.output)(tt.command); slices.Contains(got, NamedPackage) {
				t.Errorf("warnings = %v, want no %q", got, NamedPackage)
			}
		})
	}
}

func TestWarningsForKeepsTheOtherWarnings(t *testing.T) {
	got := WarningsFor("No module named 'foo'")("sudo pip install foo")

	if want := []string{Elevated, NamedPackage}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v, want %v", got, want)
	}
}

func TestWarningsForWithoutOutputMatchesWarnings(t *testing.T) {
	for _, cmd := range []string{"pip install foo", "sudo make install", "git status"} {
		if got, want := WarningsFor("")(cmd), Warnings(cmd); !slices.Equal(got, want) {
			t.Errorf("WarningsFor(\"\")(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestWarningsForHandlesMalformedInput(t *testing.T) {
	output := "No module named 'foo' " + strings.Repeat("x", 1<<16)
	commands := []string{
		"", "pip", "pip install", "python -m", "python -m pip", "uv", "npm i '", `pip install "`,
		"pip install ==1.0", "npm install @", "pip install --index-url", "&& ; | ||",
		"pip install \x1b[31mfoo\x1b[0m",
		"pip install " + strings.Repeat("a", 1<<16),
		strings.Repeat("pip install foo && ", 1000),
	}
	for _, cmd := range commands {
		t.Run(strings.ToValidUTF8(cmd[:min(len(cmd), 40)], "?"), func(*testing.T) {
			_ = WarningsFor(output)(cmd)
		})
	}
}
