package buildinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Build the real binaries with the Dockerfile's linker flag: a renamed variable
// or a dropped flag must fail here instead of shipping "(devel)" images.
func TestReleaseVersionFromLinkerFlag(t *testing.T) {
	const flag = "-X github.com/Sociaro-Research/open-media-gateway/internal/buildinfo.version="
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(dockerfile), flag+"${VERSION}"); n != 2 {
		t.Fatalf("Dockerfile sets the release version for %d binaries, want gateway and worker", n)
	}
	for _, name := range []string{"gateway", "worker"} {
		bin := filepath.Join(t.TempDir(), name)
		if out, err := exec.Command("go", "build", "-ldflags", flag+"v9.8.7-test", "-o", bin, "../../cmd/"+name).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
		cmd := exec.Command(bin, "--version")
		cmd.Env = []string{}
		out, err := cmd.Output()
		if err != nil || !strings.HasPrefix(string(out), name+" v9.8.7-test") {
			t.Fatalf("%s --version = %q, %v", name, out, err)
		}
	}
}
