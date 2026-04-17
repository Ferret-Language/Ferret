package packages

import "testing"

func Test_packageArchiveURL(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		repoName string
		version  string
		want     string
		wantErr  bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := packageArchiveURL(tt.repoName, tt.version)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("packageArchiveURL() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("packageArchiveURL() succeeded unexpectedly")
			}
			// TODO: update the condition below to compare got with tt.want.
			if true {
				t.Errorf("packageArchiveURL() = %v, want %v", got, tt.want)
			}
		})
	}
}
