package main

import (
	"flag"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller"
)

var setupLog = ctrl.Log.WithName("setup")

// DefaultOptions returns the default values for the program options.
func DefaultOptions() controller.Options {
	return controller.Options{
		LogLevel:             "info",
		ExtProcImage:         "ghcr.io/envoyproxy/ai-gateway-extproc:latest",
		EnableLeaderElection: false,
	}
}

// GetOptions parses the program flags and returns them as Options.
func GetOptions() controller.Options {
	opts := DefaultOptions()
	flag.StringVar(&opts.LogLevel, "logLevel", opts.LogLevel, "The log level")
	flag.StringVar(&opts.ExtProcImage, "extprocImage", opts.ExtProcImage, "The image for the external processor")
	flag.BoolVar(&opts.EnableLeaderElection, "leader-elect", opts.EnableLeaderElection,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	zapOpts := zap.Options{
		Development: true,
	}
	zapOpts.BindFlags(flag.CommandLine)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	flag.Parse()
	return opts
}

func main() {
	options := GetOptions()
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get k8s config")
	}

	// TODO: starts the extension server?

	if err := controller.StartControllers(k8sConfig, setupLog, options); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}
