//go:build e2e

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

package framework

import (
	"errors"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilnet "k8s.io/apimachinery/pkg/util/net"
)

// isTransientError reports whether err looks like a transient condition -
// an API server hiccup (timeout, 429, 503, 500) or a network-level blip
// (connection reset/refused, an unexpected EOF, the same signatures used by
// ExecInPod's SPDY stream) - as opposed to a terminal failure: a malformed
// rest.Config, an exec executor that couldn't even be built, an RBAC
// denial, or a command that genuinely failed inside the pod.
//
// Only errors classified transient here make a poll iteration retry ("not
// ready yet"); every other error aborts the wait immediately instead of
// silently retrying a bad request until the timeout fires. NotFound is
// intentionally not handled here: its meaning (success vs. "keep waiting")
// depends on what the caller is polling for, so each waiter classifies it
// itself.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) {
		return true
	}

	if utilnet.IsConnectionReset(err) || utilnet.IsConnectionRefused(err) || utilnet.IsProbableEOF(err) {
		return true
	}

	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
