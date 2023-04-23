/*
Copyright 2023 Serge Logvinov.

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
	"flag"
	"fmt"
	"net"
	"os"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"

	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

var (
	showVersion = flag.Bool("version", false, "Print the version and exit.")

	master     = flag.String("master", "", "Master URL to build a client config from. Either this or kubeconfig needs to be set if the provisioner is being run out of cluster.")
	kubeconfig = flag.String("kubeconfig", "", "Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.")
	// cluster     = flag.String("cluster", "", "The identifier of the cluster that the plugin is running in.")

	csiEndpoint = flag.String("csi-address", "unix:///csi/csi.sock", "CSI Endpoint")
	// cloudconfig = flag.String("cloud-config", "", "The path to the CSI driver cloud config.")

	nodeID = flag.String("node-id", "", "Node name")
)

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true") // nolint: errcheck
	flag.Parse()

	if *showVersion {
		info, err := csi.GetVersionJSON()
		if err != nil {
			klog.Fatalln(err)
		}

		fmt.Println(info)
		os.Exit(0)
	}

	kubeconfigEnv := os.Getenv("KUBECONFIG")
	if kubeconfigEnv != "" {
		klog.Infof("Found KUBECONFIG environment variable set, using that..")

		kubeconfig = &kubeconfigEnv
	}

	var (
		config *rest.Config
		err    error
	)

	if *master != "" || *kubeconfig != "" {
		klog.Infof("Either master or kubeconfig specified. building kube config from that..")

		config, err = clientcmd.BuildConfigFromFlags(*master, *kubeconfig)
		if err != nil {
			klog.Fatal(err)
		}
	} else {
		klog.Infof("Building kube configs for running in cluster...")

		config, err = rest.InClusterConfig()
		if err != nil {
			klog.Fatal(err)
		}
	}

	clientset, err := clientkubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	if *csiEndpoint == "" {
		klog.Fatalln("csi-address must be provided")
	}

	// if *cloudconfig == "" {
	// 	klog.Fatalln("cloud-config must be provided")
	// }

	nodeName := *nodeID
	if nodeName == "" {
		nodeName = os.Getenv("NODE_NAME")

		if nodeName == "" {
			klog.Fatalln("node-id or NODE_NAME environment must be provided")
		}
	}

	scheme, addr, err := csi.ParseEndpoint(*csiEndpoint)
	if err != nil {
		klog.Fatalf("Failed to parse endpoint: %v", err)
	}

	listener, err := net.Listen(scheme, addr)
	if err != nil {
		klog.Fatalf("Failed to listen on %s: %v", *csiEndpoint, err)
	}

	logErr := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
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

	// controllerService, err := csi.NewControllerService(*cloudconfig)
	// if err != nil {
	// 	klog.Fatalf("Failed to create controller service: %v", err)
	// }

	identityService := csi.NewIdentityService()
	nodeService := csi.NewNodeService(nodeName, clientset)

	// proto.RegisterControllerServer(srv, controllerService)
	proto.RegisterIdentityServer(srv, identityService)
	proto.RegisterNodeServer(srv, nodeService)

	klog.Infof("Listening for connection on address: %#v", listener.Addr())

	if err := srv.Serve(listener); err != nil {
		klog.Fatalf("Failed to serve: %v", err)
	}
}
