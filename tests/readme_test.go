package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadme runs every "$ " line in the README's indented code blocks from
// the repository root and compares its output with the lines that follow.
func TestReadme(t *testing.T) {
	lines := strings.Split(readFile(t, filepath.Join(repoRoot, "README.md")), "\n")
	found := 0
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "    $ ") {
			continue
		}
		cmdline := strings.TrimPrefix(lines[i], "    $ ")
		var want []string
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			if l == "" && j+1 < len(lines) && strings.HasPrefix(lines[j+1], "    ") && !strings.HasPrefix(lines[j+1], "    $ ") {
				want = append(want, "")
				continue
			}
			if !strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "    $ ") {
				break
			}
			want = append(want, strings.TrimPrefix(l, "    "))
		}
		found++
		var cmd *exec.Cmd
		const tool = "go run ./plugin/scripts "
		switch {
		case strings.HasPrefix(cmdline, tool):
			cmd = exec.Command(binary, strings.Fields(strings.TrimPrefix(cmdline, tool))...)
		case strings.HasPrefix(cmdline, "go -C examples/shop run "):
			cmd = exec.Command("go", strings.Fields(strings.TrimPrefix(cmdline, "go "))...)
		default:
			t.Errorf("README command this test doesn't know how to run: %s", cmdline)
			continue
		}
		cmd.Dir = repoRoot
		cmd.Env = goEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: %v", cmdline, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != strings.TrimRight(strings.Join(want, "\n"), "\n") {
			t.Errorf("%s: README output differs.\n--- got\n%s\n--- README\n%s", cmdline, got, strings.Join(want, "\n"))
		}
	}
	if found < 3 {
		t.Errorf("found %d commands in the README, expected at least 3", found)
	}
}

// TestReadmeRefusal makes the type error the README describes and checks
// the message it quotes.
func TestReadmeRefusal(t *testing.T) {
	dir := shopCopy(t)
	p := filepath.Join(dir, "internal", "auth", "auth.go")
	writeFile(t, p, readFile(t, p)+"var _ int = \"x\"\n")
	r := runTool(t, dir)
	if r.code != 2 || r.stdout != "" {
		t.Fatalf("exit %d, stdout %q", r.code, r.stdout)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(strings.ReplaceAll(r.stderr, real, "/path/to/examples/shop"))
	if !strings.Contains(readFile(t, filepath.Join(repoRoot, "README.md")), "    "+got+"\n") {
		t.Errorf("the README doesn't quote this message:\n%s", got)
	}
}
