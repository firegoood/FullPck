package app

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The one-line install the Iran server prints for a kharej without FullPack:
//
//	bash <(curl -fsSL …/install.sh) link apply 'fullpack://…'
//
// install.sh has to take exactly that — with or without a leading "fullpack" —
// hand it to the installed binary instead of opening the menu, and go on
// refusing anything else. The argument block is run here as the script runs it.
func TestTheInstallerTakesASetupLinkAndNothingElse(t *testing.T) {
	src, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh := string(src)
	start := strings.Index(sh, `ROLE=""`)
	end := strings.Index(sh, "# Process-substitution file descriptors")
	if start < 0 || end < start {
		t.Fatal("install.sh no longer has the argument block this test runs")
	}
	block := "set -euo pipefail\nerr() { echo \"$*\" >&2; }\n" + sh[start:end] +
		"\nprintf '%s\\n' \"$ROLE\" \"${BP_ARGS[@]}\"\n"

	run := func(args ...string) (string, int) {
		cmd := exec.Command("bash", append([]string{"-c", block, "install.sh"}, args...)...)
		out, err := cmd.Output()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out)), code
	}

	const link = "fullpack://H4sIAAAAAAAC_abc"
	for _, args := range [][]string{
		{"link", "apply", link},
		{"fullpack", "link", "apply", link},
	} {
		out, code := run(args...)
		if code != 0 || out != "kharej\nlink\napply\n"+link {
			t.Errorf("%q: exit %d, handed on %q", args, code, out)
		}
	}
	if out, code := run(); code != 0 || out != "" {
		t.Errorf("no arguments: exit %d, handed on %q — a plain install must stay plain", code, out)
	}
	for _, role := range []string{"iran", "kharej"} {
		if out, code := run("--role", role); code != 0 || out != role {
			t.Errorf("role %q: exit %d, output %q", role, code, out)
		}
	}
	if out, code := run("--role", "kharej", "link", "apply", link); code != 0 ||
		out != "kharej\nlink\napply\n"+link {
		t.Errorf("explicit role and link: exit %d, output %q", code, out)
	}
	for _, args := range [][]string{{"foo"}, {"link"}, {"link", "apply"}, {"node", "--panel", "x"}} {
		if _, code := run(args...); code != 2 {
			t.Errorf("%q was accepted (exit %d)", args, code)
		}
	}

	if !strings.Contains(sh, `exec "$BIN_PATH" "${BP_ARGS[@]}"`) {
		t.Error("install.sh no longer hands the setup link to the installed binary")
	}
	if apply, leave := strings.Index(sh, `exec "$BIN_PATH" "${BP_ARGS[@]}"`),
		strings.Index(sh, `if [[ "$ROLE" == "kharej" ]]; then`); apply < 0 || leave < 0 || apply > leave {
		t.Error("a Kharej setup link must be applied before the installer exits for that role")
	}
}
