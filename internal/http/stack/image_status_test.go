package stack

import "testing"

func TestParseManifestInspectDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "single object descriptor digest",
			input: `{"Descriptor":{"digest":"sha256:abc123"},"Digest":"sha256:def456"}`,
			want:  "sha256:abc123",
		},
		{
			name:  "single object digest fallback",
			input: `{"Descriptor":{"digest":""},"Digest":"sha256:def456"}`,
			want:  "sha256:def456",
		},
		{
			name:  "array digest",
			input: `[{"Descriptor":{"digest":"sha256:aaa111"}}]`,
			want:  "sha256:aaa111",
		},
		{
			name:    "invalid payload",
			input:   `{`,
			wantErr: true,
		},
		{
			name:    "missing digest",
			input:   `{"Descriptor":{"digest":""},"Digest":""}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseManifestInspectDigest(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseManifestInspectDigest returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseManifestInspectDigest=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestDigestFromRepoDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid repo digest",
			input: "docker.io/library/nginx@sha256:1234abcd",
			want:  "sha256:1234abcd",
		},
		{
			name:  "uppercase digest normalized",
			input: "docker.io/library/nginx@SHA256:ABCD",
			want:  "sha256:abcd",
		},
		{
			name:  "missing digest",
			input: "docker.io/library/nginx:latest",
			want:  "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := digestFromRepoDigest(tt.input)
			if got != tt.want {
				t.Fatalf("digestFromRepoDigest=%q, want %q", got, tt.want)
			}
		})
	}
}
