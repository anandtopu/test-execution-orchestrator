package runsvc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/model"
)

func TestValidateCreateRun(t *testing.T) {
	t.Parallel()

	valid := func() *model.CreateRunRequest {
		return &model.CreateRunRequest{
			RepoFullName: "org/repo",
			CommitSHA:    "deadbeef",
			Branch:       "main",
			Manifest: model.TestManifest{
				Runner: "pytest",
				Tests:  []model.TestEntry{{Path: "t.py", Name: "test_x"}},
			},
		}
	}

	tests := []struct {
		name       string
		mutate     func(r *model.CreateRunRequest)
		wantFields []string
	}{
		{"all valid", func(*model.CreateRunRequest) {}, nil},
		{"missing repo", func(r *model.CreateRunRequest) { r.RepoFullName = "" }, []string{"repo_full_name"}},
		{"missing commit", func(r *model.CreateRunRequest) { r.CommitSHA = "" }, []string{"commit_sha"}},
		{"missing branch", func(r *model.CreateRunRequest) { r.Branch = "" }, []string{"branch"}},
		{"missing runner", func(r *model.CreateRunRequest) { r.Manifest.Runner = "" }, []string{"manifest.runner"}},
		{"no tests", func(r *model.CreateRunRequest) { r.Manifest.Tests = nil }, []string{"manifest.tests"}},
		{
			"all empty",
			func(r *model.CreateRunRequest) { *r = model.CreateRunRequest{} },
			[]string{"repo_full_name", "commit_sha", "branch", "manifest.runner", "manifest.tests"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := valid()
			tc.mutate(r)
			got := validateCreateRun(r)
			gotFields := make([]string, len(got))
			for i, fe := range got {
				gotFields[i] = fe.Field
			}
			require.ElementsMatch(t, tc.wantFields, gotFields)
		})
	}
}

func TestValidationError(t *testing.T) {
	t.Parallel()

	t.Run("empty fields", func(t *testing.T) {
		t.Parallel()
		e := &ValidationError{}
		require.Equal(t, "validation failed", e.Error())
	})

	t.Run("counts fields", func(t *testing.T) {
		t.Parallel()
		e := &ValidationError{Fields: []model.FieldError{
			{Field: "a", Message: "required"},
			{Field: "b", Message: "required"},
		}}
		require.Equal(t, "validation failed: 2 field error(s)", e.Error())
	})

	t.Run("unwraps to ErrValidation", func(t *testing.T) {
		t.Parallel()
		var e error = &ValidationError{Fields: []model.FieldError{{Field: "a"}}}
		require.ErrorIs(t, e, ErrValidation)

		var ve *ValidationError
		require.True(t, errors.As(e, &ve))
		require.Len(t, ve.Fields, 1)
	})
}

func TestNullableString(t *testing.T) {
	t.Parallel()
	require.Nil(t, nullableString(""))
	require.Equal(t, "x", nullableString("x"))
}
