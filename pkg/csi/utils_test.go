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

package csi_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
)

func TestParseEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg            string
		endpoint       string
		expectedScheme string
		expectedAddr   string
		expectedError  error
	}{
		{
			msg:            "unix socket",
			endpoint:       "unix://tmp/csi.sock",
			expectedScheme: "unix",
			expectedAddr:   "/tmp/csi.sock",
		},
		{
			msg:           "http",
			endpoint:      "http://tmp/csi.sock",
			expectedError: fmt.Errorf("unsupported protocol: http"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			scheme, addr, err := csi.ParseEndpoint(testCase.endpoint)

			if testCase.expectedError != nil {
				assert.NotNil(t, err)
				assert.Equal(t, err.Error(), testCase.expectedError.Error())
			} else {
				assert.Nil(t, err)
				assert.NotNil(t, scheme, testCase.expectedScheme)
				assert.Equal(t, addr, testCase.expectedAddr)
			}
		})
	}
}
