package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/version"
)

func TestVersion_DefaultDev(t *testing.T) {
	var buf bytes.Buffer
	Stdout = &buf
	defer func() { Stdout = os.Stdout }()

	versionCmd.Run(versionCmd, nil)

	got := buf.String()
	want := "kubolt dev\n"
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestVersion_InjectedVersion(t *testing.T) {
	original := version.Version
	version.Version = "v9.9.9"
	defer func() { version.Version = original }()

	var buf bytes.Buffer
	Stdout = &buf
	defer func() { Stdout = os.Stdout }()

	versionCmd.Run(versionCmd, nil)

	got := buf.String()
	want := "kubolt v9.9.9\n"
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}
