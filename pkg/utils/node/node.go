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

package node

import (
	"fmt"
	"strconv"
	"strings"
)

// ID is the node ID magic string for CSI Node ID.
type ID struct {
	id   int
	node string
}

// ParseNodeID returns the ID struct from the magic nodeID string.
func ParseNodeID(nodeID string) (n ID, err error) {
	errMsg := fmt.Errorf("NodeID must be in format <nodeName>/<vmID> or <nodeName>")

	if nodeID == "" {
		return ID{}, errMsg
	}

	vmID := 0

	parts := strings.SplitN(nodeID, "/", 2)
	if parts[0] == "" {
		return ID{}, errMsg
	}

	if len(parts) == 2 {
		vmID, err = strconv.Atoi(parts[1])
		if err != nil {
			return ID{}, errMsg
		}

		return ID{id: vmID, node: parts[0]}, nil
	}

	return ID{id: vmID, node: parts[0]}, nil
}

// GetNodeID returns the CSI NodeID magic string.
func GetNodeID(nodeName string) (n ID, err error) {
	info, err := GetSMBIOSInfo()
	if err != nil {
		return ParseNodeID(nodeName)
	}

	vmID := 0

	if info.SerialNumber != "" {
		options := strings.Split(info.SerialNumber, ";")
		for _, option := range options {
			parts := strings.SplitN(option, "=", 2)
			if len(parts) == 2 {
				if parts[0] == "i" {
					vmID, err = strconv.Atoi(parts[1])
					if err != nil {
						return ParseNodeID(nodeName)
					}
				}
			}
		}
	}

	return ID{id: vmID, node: nodeName}, nil
}

// GetNodeName returns the node name from the nodeID.
func (n ID) GetNodeName() string {
	return n.node
}

// GetVMID returns the VM ID from the nodeID.
func (n ID) GetVMID() (int, error) {
	if n.id != 0 {
		return n.id, nil
	}

	return 0, fmt.Errorf("NodeID does not have VM ID")
}

func (n ID) String() string {
	if n.id != 0 {
		return fmt.Sprintf("%s/%d", n.node, n.id)
	}

	return n.node
}
