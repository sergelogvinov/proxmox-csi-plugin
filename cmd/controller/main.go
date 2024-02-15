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

// Proxmox CSI Plugin Controller
package main

import (
	"context"
	"flag"
	"net"
	"os"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools"

	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

var (
	showVersion = flag.Bool("version", false, "Print the version and exit.")
	csiEndpoint = flag.String("csi-address", "unix:///csi/csi.sock", "CSI Endpoint")

	cloudconfig = flag.String("cloud-config", "", "The path to the CSI driver cloud config.")
	kubeconfig  = flag.String("kubeconfig", "", "Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.")

	version string
)

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true") //nolint: errcheck
	flag.Parse()

	klog.V(2).Infof("Driver version %v, GitVersion %s", csi.DriverVersion, version)
	klog.V(2).Info("Driver CSI Spec version: ", csi.DriverSpecVersion)

	if *showVersion {
		klog.Infof("Driver version %v, GitVersion %s", csi.DriverVersion, version)
		os.Exit(0)
	}

	if *csiEndpoint == "" {
		klog.Fatalln("csi-address must be provided")
	}

	if *cloudconfig == "" {
		klog.Fatalln("cloud-config must be provided")
	}

	kconfig, _, err := tools.BuildConfig(*kubeconfig, "")
	if err != nil {
		klog.Fatalf("failed to create kubernetes config: %v", err)
	}

	clientset, err := clientkubernetes.NewForConfig(kconfig)
	if err != nil {
		klog.Fatalf("failed to create kubernetes client: %v", err)
	}

	scheme, addr, err := csi.ParseEndpoint(*csiEndpoint)
	if err != nil {
		klog.Fatalf("Failed to parse endpoint: %v", err)
	}

	listener, err := net.Listen(scheme, addr)
	if err != nil {
		klog.Fatalf("Failed to listen on %s: %v", *csiEndpoint, err)
	}

	logErr := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, rpcerr := handler(ctx, req)
		if rpcerr != nil {
			klog.Errorf("GRPC error: %v", rpcerr)
		}

		return resp, rpcerr
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErr),
	}

	srv := grpc.NewServer(opts...)

	identityService := csi.NewIdentityService()

	controllerService, err := csi.NewControllerService(clientset, *cloudconfig)
	if err != nil {
		klog.Fatalf("Failed to create controller service: %v", err)
	}

	proto.RegisterControllerServer(srv, controllerService)
	proto.RegisterIdentityServer(srv, identityService)

	klog.Infof("Listening for connection on address: %#v", listener.Addr())

	if err := srv.Serve(listener); err != nil {
		klog.Fatalf("Failed to serve: %v", err)
	}
}
