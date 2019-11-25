package main

import (
	"flag"
	"os"

	"k8s.io/klog"

	"github.com/maisem/proxy-apiserver/pkg/cmd/apiserver"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/component-base/logs"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	stopCh := genericapiserver.SetupSignalHandler()
	options := apiserver.NewServerOptions(os.Stdout, os.Stderr)
	cmd := apiserver.NewCommandStartServer(options, stopCh)
	cmd.Flags().AddGoFlagSet(flag.CommandLine)
	if err := cmd.Execute(); err != nil {
		klog.Fatal(err)
	}
}
