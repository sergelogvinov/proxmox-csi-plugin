/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"
)

func init() {
	l := log.New()
	l.SetOutput(io.Discard)

	logger = log.NewEntry(l)
}

// fakeHold returns the queued errors from Abort, one per call, then nil.
type fakeHold struct {
	errs  []error
	calls int
}

func (f *fakeHold) Abort(context.Context) error {
	f.calls++

	if len(f.errs) == 0 {
		return nil
	}

	err := f.errs[0]
	f.errs = f.errs[1:]

	return err
}

func TestAbortHeld(t *testing.T) {
	t.Parallel()

	transient := errors.New("conflict")

	tests := []struct {
		name      string
		errs      []error
		wantErr   error
		wantCalls int
	}{
		{name: "aborted", wantCalls: 1},
		{name: "retried once after a transient failure", errs: []error{transient}, wantCalls: 2},
		{name: "fails after the retry", errs: []error{transient, transient}, wantErr: transient, wantCalls: 2},
		{name: "in use is not retried", errs: []error{fmt.Errorf("claim a: %w", rebind.ErrInUse)}, wantErr: rebind.ErrInUse, wantCalls: 1},
		{name: "committed is not retried", errs: []error{rebind.ErrCommitted}, wantErr: rebind.ErrCommitted, wantCalls: 1},
		{
			name:      "in use mixed with another failure is retried",
			errs:      []error{errors.Join(fmt.Errorf("claim a: %w", rebind.ErrInUse), transient)},
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := &fakeHold{errs: tt.errs}

			err := abortHeld(t.Context(), held)

			if tt.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.wantErr)
			}

			assert.Equal(t, tt.wantCalls, held.calls)
		})
	}
}

// commitResults returns a commit func that returns the queued errors, one per call, then nil.
func commitResults(calls *int, errs ...error) func(context.Context) error {
	return func(context.Context) error {
		*calls++

		if len(errs) == 0 {
			return nil
		}

		err := errs[0]
		errs = errs[1:]

		return err
	}
}

func TestRetryWhileInUse(t *testing.T) {
	t.Parallel()

	other := errors.New("forbidden")

	t.Run("retries until the claims are released", func(t *testing.T) {
		t.Parallel()

		calls, waits := 0, 0
		commit := commitResults(&calls, rebind.ErrInUse, rebind.ErrInUse)

		err := retryWhileInUse(t.Context(), 5, time.Millisecond, commit, func() { waits++ })

		assert.NoError(t, err)
		assert.Equal(t, 3, calls)
		assert.Equal(t, 2, waits)
	})

	t.Run("other errors are not retried", func(t *testing.T) {
		t.Parallel()

		calls := 0
		commit := commitResults(&calls, other)

		err := retryWhileInUse(t.Context(), 5, time.Millisecond, commit, func() {})

		assert.ErrorIs(t, err, other)
		assert.Equal(t, 1, calls)
	})

	t.Run("gives up after the retries are exhausted", func(t *testing.T) {
		t.Parallel()

		calls := 0
		commit := commitResults(&calls, rebind.ErrInUse, rebind.ErrInUse, rebind.ErrInUse, rebind.ErrInUse)

		err := retryWhileInUse(t.Context(), 2, time.Millisecond, commit, func() {})

		assert.ErrorIs(t, err, rebind.ErrInUse)
		assert.Equal(t, 3, calls)
	})

	t.Run("stops waiting when the context is done", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())

		calls := 0
		commit := commitResults(&calls, rebind.ErrInUse, rebind.ErrInUse)

		err := retryWhileInUse(ctx, 5, time.Hour, commit, cancel)

		assert.ErrorIs(t, err, rebind.ErrInUse)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, calls)
	})
}

func TestRecoverySteps(t *testing.T) {
	t.Parallel()

	swap := []heldClaimRef{
		{Name: "a", UID: "uid-a", Volume: "pv-a", NewName: "b"},
		{Name: "b", UID: "uid-b", Volume: "pv-b", NewName: "a"},
	}

	steps := strings.Join(recoverySteps("ns", swap), "\n")

	// Every original is released before any claim is created, as the names cross.
	releaseA := strings.Index(steps, "If claim a with uid uid-a is still Terminating")
	releaseB := strings.Index(steps, "If claim b with uid uid-b is still Terminating")
	create := strings.Index(steps, "create it from its manifest")

	assert.GreaterOrEqual(t, releaseA, 0)
	assert.GreaterOrEqual(t, releaseB, 0)
	assert.GreaterOrEqual(t, create, 0)
	assert.Less(t, releaseA, create)
	assert.Less(t, releaseB, create)

	// The hold finalizer of a claim a pod still uses is left alone.
	assert.Contains(t, steps, "once no pod uses it")

	// A claim that already exists is not the end: its volume still has to be reserved.
	assert.Contains(t, steps, "If claim b exists with the annotation "+rebind.AnnRebindFrom+"=uid-a, it was already recreated")
	assert.Contains(t, steps, "reserve persistentvolume pv-a for claim b, even if the claim already exists")
	assert.Contains(t, steps, "reserve persistentvolume pv-b for claim a, even if the claim already exists")
	assert.Contains(t, steps, rebind.AnnReclaimPolicy)
	assert.Contains(t, steps, "If claim a with uid uid-a is still Terminating: once no pod uses it, remove the "+rebind.Finalizer+" finalizer")

	// A claim that keeps its name and was never deleted only needs the finalizer removed.
	same := strings.Join(recoverySteps("ns", []heldClaimRef{{Name: "a", UID: "uid-a", Volume: "pv-a"}}), "\n")
	assert.Contains(t, same, "If claim a with uid uid-a exists and is not Terminating")
}

func TestOnlyInUse(t *testing.T) {
	t.Parallel()

	inUse := fmt.Errorf("claim a: %w", rebind.ErrInUse)
	other := errors.New("conflict")

	assert.True(t, onlyInUse(inUse))
	assert.True(t, onlyInUse(errors.Join(inUse, fmt.Errorf("claim b: %w", rebind.ErrInUse))))
	assert.True(t, onlyInUse(fmt.Errorf("hold: %w", errors.Join(inUse))))
	assert.False(t, onlyInUse(errors.Join(inUse, other)))
	assert.False(t, onlyInUse(fmt.Errorf("hold: %w; release: %w", other, inUse)))
	assert.False(t, onlyInUse(other))
	assert.False(t, onlyInUse(nil))
}

func TestUniquePods(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"p1", "p2", "p3"}, uniquePods([]string{"p1", "p2"}, []string{"p2", "p3"}))
	assert.Empty(t, uniquePods(nil, nil))
}
