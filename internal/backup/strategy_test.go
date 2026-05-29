package backup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

// dummyStrategy is a test implementation of Strategy.
type dummyStrategy struct{}

func (d *dummyStrategy) Backup(ctx context.Context, app manifest.App, target manifest.Target, localTsDir string) error {
	return nil
}

func TestResolveStrategy_Unknown(t *testing.T) {
	b := &Backuper{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	_, err := b.resolveStrategy("bogus")
	if err == nil {
		t.Fatal("expected error for unknown strategy type, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error to contain 'bogus', got: %v", err)
	}
}

func TestResolveStrategy_Registered(t *testing.T) {
	// Register a dummy strategy for this test
	testType := manifest.TargetType("test_dummy")
	registerStrategy(testType, func(b *Backuper) Strategy {
		return &dummyStrategy{}
	})

	b := &Backuper{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	strategy, err := b.resolveStrategy(testType)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if strategy == nil {
		t.Fatal("expected non-nil strategy")
	}
	if _, ok := strategy.(*dummyStrategy); !ok {
		t.Fatalf("expected *dummyStrategy, got %T", strategy)
	}
}
