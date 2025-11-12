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

package node_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/node"
)

func TestNodeID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg       string
		nodeID    string
		VMName    string
		VMNameErr error
		VMID      int
		VMIDErr   error
	}{
		{
			msg:     "Test Node ID",
			nodeID:  "node-123",
			VMName:  "node-123",
			VMID:    0,
			VMIDErr: fmt.Errorf("NodeID does not have VM ID"),
		},
		{
			msg:     "Test Node ID with VM ID",
			nodeID:  "node-123/456",
			VMName:  "node-123",
			VMID:    456,
			VMIDErr: nil,
		},
		{
			msg:       "Test Node ID with invalid VM ID",
			nodeID:    "node-123/abc",
			VMName:    "",
			VMNameErr: fmt.Errorf("NodeID must be in format <nodeName>/<vmID> or <nodeName>"),
			VMID:      0,
			VMIDErr:   fmt.Errorf("NodeID does not have VM ID"),
		},
		{
			msg:       "Test Node ID without node name",
			nodeID:    "/456",
			VMName:    "",
			VMNameErr: fmt.Errorf("NodeID must be in format <nodeName>/<vmID> or <nodeName>"),
			VMID:      0,
			VMIDErr:   fmt.Errorf("NodeID does not have VM ID"),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.msg, func(t *testing.T) {
			t.Parallel()

			n, err := node.ParseNodeID(testCase.nodeID)
			id, errID := n.GetVMID()

			if testCase.VMNameErr != nil {
				assert.EqualError(t, err, testCase.VMNameErr.Error())
			} else {
				assert.Nil(t, err)
			}

			assert.Equal(t, testCase.VMName, n.GetNodeName())

			if testCase.VMIDErr != nil {
				assert.EqualError(t, errID, testCase.VMIDErr.Error())
			} else {
				assert.Nil(t, errID)
			}

			assert.Equal(t, testCase.VMID, id)
		})
	}
}
