package installer

import (
	"context"
	"fmt"
	"testing"
)

func TestTreeExecutor_Stub(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		got := fmt.Sprintf("%v", r)
		if got != "not implemented — see Task 7" {
			t.Fatalf("unexpected panic msg: %q", got)
		}
	}()
	ex := TreeExecutor{Parallelism: 1}
	ex.Run(context.Background(), TreePlan{})
}
