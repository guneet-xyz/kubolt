package backup

import (
	"context"
	"fmt"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

// Strategy executes one backup target.
type Strategy interface {
	Backup(ctx context.Context, app manifest.App, target manifest.Target, localTsDir string) error
}

// strategyFactory creates a Strategy bound to a Backuper.
type strategyFactory func(b *Backuper) Strategy

// strategies is the registry of known backup strategies.
var strategies = map[manifest.TargetType]strategyFactory{}

// registerStrategy registers a factory for the given target type.
// Called from each strategy's init().
func registerStrategy(t manifest.TargetType, f strategyFactory) {
	strategies[t] = f
}

// resolveStrategy returns a Strategy for the given target type, or an error.
func (b *Backuper) resolveStrategy(t manifest.TargetType) (Strategy, error) {
	f, ok := strategies[t]
	if !ok {
		return nil, fmt.Errorf("unknown backup target type %q", t)
	}
	return f(b), nil
}
