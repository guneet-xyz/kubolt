package backup

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolvePod(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		wantPod   string
		wantErr   bool
		errSubstr []string
	}{
		{
			name:    "single_pod",
			stdout:  "postgres-0 ",
			wantPod: "postgres-0",
		},
		{
			name:      "no_pods",
			stdout:    "",
			wantErr:   true,
			errSubstr: []string{"no Running pod", "app=postgres"},
		},
		{
			name:      "multi_pod",
			stdout:    "postgres-0 postgres-1 postgres-2 ",
			wantErr:   true,
			errSubstr: []string{"postgres-0", "postgres-1", "postgres-2", "narrow"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStub()
			stub.setOutput("kubectl get pod -n test-ns", tc.stdout)
			SetExecCommand(stub.execFn())
			defer ResetExecCommand()

			var stdout, stderr bytes.Buffer
			b := newBackuper(&stdout, &stderr)

			got, err := resolvePod(b, "test-ns", "app=postgres")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got pod=%q", got)
				}
				for _, s := range tc.errSubstr {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q missing substring %q", err.Error(), s)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantPod {
				t.Errorf("got pod %q, want %q", got, tc.wantPod)
			}
		})
	}
}

func TestResolveDatabase(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(s *stubRecorder)
		wantDB    string
		wantCalls int
		wantErr   bool
		errSubstr []string
	}{
		{
			name: "found_at_PGDATABASE",
			setup: func(s *stubRecorder) {
				s.setOutput("kubectl exec -n test-ns pg-pod -- printenv PGDATABASE", "mydb\n")
			},
			wantDB:    "mydb",
			wantCalls: 1,
		},
		{
			name: "found_at_POSTGRES_DB",
			setup: func(s *stubRecorder) {
				s.setFailure("kubectl exec -n test-ns pg-pod -- printenv PGDATABASE", 1)
				s.setOutput("kubectl exec -n test-ns pg-pod -- printenv POSTGRES_DB", "appdb\n")
			},
			wantDB:    "appdb",
			wantCalls: 2,
		},
		{
			name: "found_at_POSTGRESQL_DATABASE",
			setup: func(s *stubRecorder) {
				s.setFailure("kubectl exec -n test-ns pg-pod -- printenv PGDATABASE", 1)
				s.setFailure("kubectl exec -n test-ns pg-pod -- printenv POSTGRES_DB", 1)
				s.setOutput("kubectl exec -n test-ns pg-pod -- printenv POSTGRESQL_DATABASE", "legacydb\n")
			},
			wantDB:    "legacydb",
			wantCalls: 3,
		},
		{
			name: "none_set",
			setup: func(s *stubRecorder) {
				s.setFailure("kubectl exec -n test-ns pg-pod -- printenv PGDATABASE", 1)
				s.setFailure("kubectl exec -n test-ns pg-pod -- printenv POSTGRES_DB", 1)
				s.setFailure("kubectl exec -n test-ns pg-pod -- printenv POSTGRESQL_DATABASE", 1)
			},
			wantErr:   true,
			errSubstr: []string{"PGDATABASE", "POSTGRES_DB", "POSTGRESQL_DATABASE"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStub()
			tc.setup(stub)
			SetExecCommand(stub.execFn())
			defer ResetExecCommand()

			var stdout, stderr bytes.Buffer
			b := newBackuper(&stdout, &stderr)

			got, err := resolveDatabase(b, "test-ns", "pg-pod")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got db=%q", got)
				}
				for _, s := range tc.errSubstr {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q missing substring %q", err.Error(), s)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantDB {
				t.Errorf("got db %q, want %q", got, tc.wantDB)
			}
			if calls := stub.getCalls(); len(calls) != tc.wantCalls {
				t.Errorf("got %d calls, want %d: %v", len(calls), tc.wantCalls, calls)
			}
		})
	}
}
