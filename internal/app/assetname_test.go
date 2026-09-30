package app

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// Legacy 32-bit ARM builds still name their own variant. New releases after
// v1.8.7 publish only amd64 and arm64; this behavior remains for source builds
// and older installations.
//
// runtime.GOARCH is "arm" for every 32-bit ARM build, whichever variant it was
// compiled for, and the three are not interchangeable: a v7 binary on a v5
// board is an illegal instruction, not a slow one. Releases through v1.8.7
// named them apart; a source build also needs to preserve that distinction
// when looking up an older archive.
func TestAnARMBuildAsksForItsOwnVariant(t *testing.T) {
	saved := GOARM
	t.Cleanup(func() { GOARM = saved })

	if runtime.GOARCH == "arm" {
		for _, v := range []string{"5", "6", "7"} {
			GOARM = v
			if got := AssetArch(); got != "armv"+v {
				t.Errorf("GOARM=%s gave %q, want armv%s", v, got, v)
			}
		}
		// Unstamped — a plain `go build`, which has no published asset of its
		// own. Falling back to the bare GOARCH is right there.
		GOARM = ""
		if got := AssetArch(); got != "arm" {
			t.Errorf("an unstamped arm build gave %q, want arm", got)
		}
		return
	}

	// On every other architecture the stamp is ignored, so a stray value in the
	// build cannot rename the asset.
	GOARM = "7"
	if got := AssetArch(); got != runtime.GOARCH {
		t.Errorf("a %s build with a stray GOARM gave %q", runtime.GOARCH, got)
	}
	if !strings.HasPrefix(AssetName(), "fullpack_linux_"+runtime.GOARCH) {
		t.Errorf("AssetName = %q", AssetName())
	}
}

// The release build has to publish an asset for every architecture it builds,
// under exactly the name a binary of that architecture will ask for.
func TestEveryArchitectureBuiltIsAlsoPublished(t *testing.T) {
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Skipf("no Makefile here: %v", err)
	}
	src := string(mk)

	if !strings.Contains(strings.ReplaceAll(src, "\r\n", "\n"), "ARCHES := amd64 arm64\n") {
		t.Error("future releases must build only Linux amd64 and arm64")
	}
	if strings.Contains(src, "ARMS   :=") || strings.Contains(src, "GOARCH=arm ") {
		t.Error("the release build still includes 32-bit ARM variants")
	}
	// Only the two listed binaries are packaged, and stale archives are removed
	// before checksums are written so a repeated local build cannot publish more.
	if !strings.Contains(src, "release/fullpack_linux_$$a.tar.gz") {
		t.Error("the archives are not named after the architectures that were built")
	}
	if !strings.Contains(src, "rm -f release/fullpack_linux_*.tar.gz") {
		t.Error("old release archives can leak into the next SHA256SUMS")
	}
}

// And the installer can pick the right one for the machine it lands on.
func TestTheInstallerKnowsEveryPublishedArchitecture(t *testing.T) {
	sh, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Skipf("no install.sh here: %v", err)
	}
	src := string(sh)

	for _, want := range []string{"amd64", "arm64"} {
		if !strings.Contains(src, want) {
			t.Errorf("install.sh cannot resolve %q, so that release asset is "+
				"published and unreachable", want)
		}
	}
	// v6 is the safe default when the variant cannot be read: it runs on v6 and
	// v7 both, where a v7 guess on a v6 board does not run at all.
	if !strings.Contains(src, "*)   echo 6 ;;") {
		t.Error("an ARM machine whose variant cannot be determined gets no safe default")
	}
}

// What is built has to be what is published.
//
// The workflow must publish exactly the two supported release archives.
func TestTheWorkflowPublishesEverythingTheBuildProduces(t *testing.T) {
	wf, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Skipf("no workflow here: %v", err)
	}
	src := string(wf)
	for _, want := range []string{"release/fullpack_linux_amd64.tar.gz", "release/fullpack_linux_arm64.tar.gz"} {
		if !strings.Contains(src, want) {
			t.Errorf("the workflow omits %s", want)
		}
	}
	if strings.Contains(src, "release/fullpack_linux_*.tar.gz") {
		t.Error("the workflow wildcard could publish an unsupported archive")
	}
	if !strings.Contains(src, "release/SHA256SUMS") {
		t.Error("the checksums are not published, and the updater refuses an archive " +
			"it cannot verify")
	}
}
