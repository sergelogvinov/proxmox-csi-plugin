/*
Copyright 2023 sergelogvinov.

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

package csi

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"

	"k8s.io/klog/v2"
)

const (
	// DriverName to be registered
	DriverName = "csi.proxmox.com"
)

type Driver struct {
	controllerService
	nodeService

	srv      *grpc.Server
	endpoint string
}

// NewDriver creates a new driver
func NewDriver(endpoint string, nodeID string) *Driver {
	klog.Infof("Driver: %v version %v commit %v date %v", DriverName, driverVersion, gitCommit, buildDate)

	node, err := newNodeService(nodeID)
	if err != nil {
		klog.Fatalf("Failed to create node service: %v", err)

		return nil
	}

	controller, err := newControllerService()
	if err != nil {
		klog.Fatalf("Failed to create controller service: %v", err)

		return nil
	}

	return &Driver{
		endpoint:          endpoint,
		controllerService: controller,
		nodeService:       node,
	}
}

func (d *Driver) Run() error {
	scheme, addr, err := ParseEndpoint(d.endpoint)
	if err != nil {
		return err
	}

	listener, err := net.Listen(scheme, addr)
	if err != nil {
		return err
	}

	logErr := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			klog.Errorf("GRPC error: %v", err)
		}
		return resp, err
	}
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErr),
	}
	d.srv = grpc.NewServer(opts...)

	csi.RegisterIdentityServer(d.srv, d)
	csi.RegisterControllerServer(d.srv, d)
	csi.RegisterNodeServer(d.srv, d)

	klog.Infof("Listening for connection on address: %#v", listener.Addr())
	return d.srv.Serve(listener)
}

func ParseEndpoint(endpoint string) (string, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("could not parse endpoint: %v", err)
	}

	addr := path.Join(u.Host, filepath.FromSlash(u.Path))

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "tcp":
	case "unix":
		addr = path.Join("/", addr)
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return "", "", fmt.Errorf("could not remove unix domain socket %q: %v", addr, err)
		}
	default:
		return "", "", fmt.Errorf("unsupported protocol: %s", scheme)
	}

	return scheme, addr, nil
}
